package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/gate"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/policy"
)

func personOnlyBus(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".duolog")
	b, err := bus.Open(dir, config.Wire{MaxLineBytes: 512, MaxSummaryBytes: 200})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []bus.Message{
		{From: "duolog/ask.person", To: "claude/lead.test", K: "ask", S: "read ../notes.txt", P: true},
		{From: "duolog/ask.web", To: "claude/lead.test", K: "ask", S: "net https://example.com"},
	} {
		if _, err := b.Send(m); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func bashInput(agent, command string) []byte {
	data, _ := json.Marshal(map[string]any{"session_id": "lead-" + agent, "tool_name": "Bash", "tool_input": map[string]string{"command": command}})
	return data
}

func TestLeadGuardAsksForPersonOnlyAllow(t *testing.T) {
	dir := personOnlyBus(t) // ask 1 person-only, ask 2 a web ask
	for _, cmd := range []string{"duolog allow 1", "cd x && duolog allow 1 --lead L", "scripts/dev/bin/duolog allow 1", "true; duolog --lead L allow 1"} {
		d, ok := leadGuard(dir, "claude", bashInput("claude", cmd))
		if !ok || d.Kind != policy.Ask || !strings.Contains(d.Reason, "only the person") {
			t.Fatal(cmd, d, ok)
		}
		d, ok = leadGuard(dir, "codex", bashInput("codex", cmd))
		if !ok || d.Kind != policy.Deny || !strings.Contains(d.Reason, "own terminal") {
			t.Fatal(cmd, d, ok)
		}
	}
	for _, cmd := range []string{"duolog allow 2", "duolog deny 1", "echo allow 1", "go test ./..."} {
		if _, ok := leadGuard(dir, "claude", bashInput("claude", cmd)); ok {
			t.Fatal(cmd)
		}
	}
}

func TestLeadGuardWithoutBusStillAsks(t *testing.T) {
	d, ok := leadGuard(t.TempDir(), "claude", bashInput("claude", "duolog allow 1"))
	if !ok || d.Kind != policy.Ask {
		t.Fatal(d, ok)
	}
}

func TestLeadGuardUncertainShellNamingDuolog(t *testing.T) {
	dir := personOnlyBus(t)
	if d, ok := leadGuard(dir, "claude", bashInput("claude", `eval "duolog allow 1"`)); !ok || d.Kind != policy.Ask {
		t.Fatal(d, ok)
	}
}

func TestLeadGuardReasonsAndFlags(t *testing.T) {
	dir := personOnlyBus(t)
	for _, agent := range []string{"claude", "codex"} {
		want := "ask 1 reads or writes outside the project; only the person may allow it"
		if agent == "codex" {
			want += ": they run duolog allow 1 --lead claude/lead.test in their own terminal in " + filepath.Base(filepath.Dir(dir))
		}
		// The Windows launchers are named as the worker denies name them (policy.ProgramName).
		for _, command := range []string{"duolog --lead=L allow 1", "duolog allow --lead L 1", "duolog.exe allow 1", "duolog.cmd allow 1", "DUOLOG.EXE allow 1", "DUOLOG.BAT allow 1", "duolog.ps1 allow 1", `C:\bin\duolog.ps1 allow 1`, `C:\tools\duolog.cmd allow 1`, "true && duolog allow 1; echo done", `"duolog" allow 1`} {
			if d, ok := leadGuard(dir, agent, bashInput(agent, command)); !ok || d.Reason != want {
				t.Fatal(agent, command, d, ok)
			}
		}
	}
}

func TestLeadGuardIgnoresUnrelatedAndMalformedInput(t *testing.T) {
	dir := t.TempDir()
	for _, agent := range []string{"claude", "codex", "unknown"} {
		for _, input := range [][]byte{
			[]byte(`{`), []byte(`{}`), []byte(`{"tool_name":"Bash","tool_input":{"command":5}}`),
			[]byte(`{"tool_name":"Read","tool_input":{"file_path":"duolog allow 1"}}`),
			bashInput(agent, "go test ./..."), bashInput(agent, "duolog deny 1"),
		} {
			if d, ok := leadGuard(dir, agent, input); ok {
				t.Fatal(agent, string(input), d)
			}
		}
	}
}

func TestLeadGuardWrappedAllowAndSeparator(t *testing.T) {
	dir := personOnlyBus(t)
	for _, agent := range []string{"claude", "codex"} {
		for _, command := range []string{
			"duolog allow -- 1", "duolog allow --lead L -- 1",
			"go run ./cmd/duolog allow 1", "env duolog allow 1",
			"VAR=1 duolog allow 1", "bash -c 'duolog allow 1'",
		} {
			t.Run(agent+"/"+command, func(t *testing.T) {
				want := "ask 1 reads or writes outside the project; only the person may allow it"
				kind := policy.Ask
				if agent == "codex" {
					kind = policy.Deny
					want += ": they run duolog allow 1 --lead claude/lead.test in their own terminal in " + filepath.Base(filepath.Dir(dir))
				}
				if d, ok := leadGuard(dir, agent, bashInput(agent, command)); !ok || d.Kind != kind || d.Reason != want {
					t.Fatal(d, ok)
				}
			})
		}
		if d, ok := leadGuard(dir, agent, bashInput(agent, "duolog allow -- 2")); ok {
			t.Fatal("web ask must remain with the lead:", d)
		}
	}
}

func TestLeadGuardUnknownNumberNeedsPendingPersonAsk(t *testing.T) {
	now := time.Now().UTC()
	person := bus.Message{N: 1, T: now.Format(time.RFC3339Nano), K: "ask", P: true}
	expired := person
	expired.T = now.Add(-gate.MaxAskWait - time.Minute).Format(time.RFC3339Nano)
	for _, tc := range []struct {
		name     string
		messages []bus.Message
		want     bool
	}{
		{"empty", nil, false},
		{"pending", []bus.Message{person}, true},
		{"expired", []bus.Message{expired}, false},
		{"allowed", []bus.Message{person, {N: 2, K: "allow", Ref: "1"}}, false},
		{"denied", []bus.Message{person, {N: 2, K: "deny", Ref: "1"}}, false},
		{"web", []bus.Message{{N: 1, T: now.Format(time.RFC3339Nano), K: "ask"}}, false},
		{"other-reply", []bus.Message{person, {N: 2, K: "deny", Ref: "3"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var data []byte
			for _, m := range tc.messages {
				line, err := json.Marshal(m)
				if err != nil {
					t.Fatal(err)
				}
				data = append(data, append(line, '\n')...)
			}
			if err := os.WriteFile(filepath.Join(dir, "bus.ndjson"), data, 0600); err != nil {
				t.Fatal(err)
			}
			for _, agent := range []string{"claude", "codex"} {
				for _, command := range []string{`rg -F "duolog allow" docs`, `git commit -m "duolog allow"`, `duolog allow "$N"`, `eval "$duolog allow"`} {
					d, ok := leadGuard(dir, agent, bashInput(agent, command))
					if ok != tc.want {
						t.Errorf("%s %q: %+v, guarded=%v, want %v", agent, command, d, ok, tc.want)
					}
					if !ok {
						continue
					}
					want := "this command may allow an ask that reads or writes outside the project; only the person may allow it"
					kind := policy.Ask
					if agent == "codex" {
						kind = policy.Deny
						want += ": they run duolog allow N --lead <lead id> in their own terminal in " + filepath.Base(filepath.Dir(dir))
					}
					if d.Kind != kind || d.Reason != want {
						t.Errorf("%s %q: %+v, want %s", agent, command, d, want)
					}
				}
			}
		})
	}
}

func TestLeadGuardMalformedBusStillGuards(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bus.ndjson"), []byte("malformed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"duolog allow 1", `rg -F "duolog allow" docs`} {
		if d, ok := leadGuard(dir, "claude", bashInput("claude", command)); !ok || d.Kind != policy.Ask {
			t.Fatal(command, d, ok)
		}
	}
}

func TestLeadGuardChecksOtherCommandTools(t *testing.T) {
	dir := personOnlyBus(t)
	data := []byte(`{"tool_name":"Monitor","tool_input":{"command":"duolog allow 1"}}`)
	if d, ok := leadGuard(dir, "claude", data); !ok || d.Kind != policy.Ask {
		t.Fatal(d, ok)
	}
}

func TestLeadGuardHintNamesOwnerAndProject(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sample-project", ".duolog")
	reg, err := session.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	owner := session.Lead{ID: "owner-123456", Target: config.Target{Agent: "codex"}}
	if err := reg.RegisterLead(owner); err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(dir, config.Wire{MaxLineBytes: 512, MaxSummaryBytes: 200})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Send(bus.Message{K: "ask", From: "duolog/ask.test", To: session.Address(owner), P: true}); err != nil {
		t.Fatal(err)
	}
	d, ok := leadGuard(dir, "codex", bashInput("codex", "duolog allow 1"))
	want := "they run duolog allow 1 --lead owner-123456 in their own terminal in sample-project"
	if !ok || !strings.Contains(d.Reason, want) || strings.Contains(d.Reason, filepath.Dir(dir)) {
		t.Fatal(d, ok)
	}
}

func TestLeadGuardKeepsOutsideRememberForPerson(t *testing.T) {
	dir := personOnlyBus(t)
	for _, agent := range []string{"claude", "codex"} {
		for _, command := range []string{
			`duolog remember 'cp ~/.ssh/id_rsa k'`,
			`env duolog remember 'tar czf x.tgz ~/notes'`,
		} {
			d, ok := leadGuard(dir, agent, bashInput(agent, command))
			kind := policy.Ask
			if agent == "codex" {
				kind = policy.Deny
			}
			if !ok || d.Kind != kind || !strings.Contains(d.Reason, "only the person") || agent == "codex" && !strings.Contains(d.Reason, "own terminal") {
				t.Errorf("%s %q: %+v, %v", agent, command, d, ok)
			}
		}
		for _, command := range []string{`duolog remember 'go test ./...'`, `duolog remember 'go test ./... >/dev/null'`, `duolog remember 'git log --grep=/x'`} {
			if d, ok := leadGuard(dir, agent, bashInput(agent, command)); ok {
				t.Errorf("%s %q: %+v", agent, command, d)
			}
		}
	}
}
