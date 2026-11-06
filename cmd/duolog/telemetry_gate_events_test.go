package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/record"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/telemetry"
)

// gateSentinel stands for a target, a command or an error text: none of it
// may reach telemetry.
const gateSentinel = "SENTINEL7f3a"

// gateTelemetrySetup is a worker gate in mode m with collection on in a
// temporary home. It returns the project root and the telemetry root.
func gateTelemetrySetup(t *testing.T, m mode.Mode, policy string) (*session.Registry, string, string) {
	t.Helper()
	reg, root := gateSetup(t, m)
	if policy != "" {
		if err := os.WriteFile(filepath.Join(root, ".duolog", "config.yaml"), []byte("policy:\n  ask_timeout_seconds: 1\n"+policy), 0600); err != nil {
			t.Fatal(err)
		}
	}
	home := os.Getenv("HOME")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("DUOLOG_TELEMETRY", "")
	telemetryRoot, err := telemetry.ResolveRoot(telemetryLocation())
	if err != nil || !strings.HasPrefix(telemetryRoot, home) {
		t.Fatalf("telemetry root %s outside the temporary home: %v", telemetryRoot, err)
	}
	return reg, root, telemetryRoot
}

// answerGateAsks replies to every ask on the bus with answer, as the lead's
// duolog allow or deny does, until the test ends. An empty answer leaves them.
func answerGateAsks(t *testing.T, reg *session.Registry, root, answer string) {
	t.Helper()
	if answer == "" {
		return
	}
	cfg, err := configfile.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(reg.Dir, cfg.Wire)
	if err != nil {
		t.Fatal(err)
	}
	lead, _, err := reg.Lead("lead-123456")
	if err != nil {
		t.Fatal(err)
	}
	stop, done := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(stop); <-done })
	go func() {
		defer close(done)
		answered := map[int64]bool{}
		for {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
			}
			data, _ := os.ReadFile(filepath.Join(reg.Dir, "bus.ndjson"))
			for _, line := range bytes.Split(data, []byte("\n")) {
				var m bus.Message
				if json.Unmarshal(line, &m) != nil || m.K != gate.Ask || answered[m.N] {
					continue
				}
				answered[m.N] = true
				_, _ = b.Send(bus.Message{From: session.Address(lead), To: m.From, K: answer, Ref: strconv.FormatInt(m.N, 10), S: answer})
			}
		}
	}()
}

// gateEvents reads this project's gate events.
func gateEvents(t *testing.T, root string) []telemetry.Gate {
	t.Helper()
	cfg, err := configfile.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	s, err := telemetry.Open(telemetryOptions(cfg, root))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	events, err := s.Read(telemetry.Query{})
	if err != nil {
		t.Fatal(err)
	}
	var out []telemetry.Gate
	for _, e := range events {
		if g, ok := e.(telemetry.Gate); ok {
			out = append(out, g)
		}
	}
	return out
}

// telemetryText is every file under the telemetry root, as text.
func telemetryText(t *testing.T, telemetryRoot string) string {
	t.Helper()
	var all strings.Builder
	err := filepath.WalkDir(telemetryRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() == "salt" {
			return err
		}
		data, err := os.ReadFile(path)
		all.Write(data)
		return err
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return all.String()
}

func gateHook(t *testing.T, agent, tool string, input map[string]string, root string) string {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"tool_name": tool, "cwd": root, "tool_input": input})
	out, err := gateCommand(t, string(data), "gate", "--agent", agent, "--event", "PreToolUse")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTelemetryGateAsks(t *testing.T) {
	t.Run("explicit allow emits none", func(t *testing.T) {
		for _, m := range []mode.Mode{mode.Full, mode.Edits} {
			_, root, telemetryRoot := gateTelemetrySetup(t, m, "")
			// Full mode confirms the unmatched command without an ask.
			for _, command := range []string{"go test ./...", "custom-command"} {
				if m != mode.Full && command == "custom-command" {
					continue
				}
				if out := gateHook(t, "codex", "Bash", map[string]string{"command": command}, root); out != "" {
					t.Fatalf("%s %s: %s", m, command, out)
				}
			}
			// An allow opens no store at all.
			if _, err := os.Stat(telemetryRoot); !os.IsNotExist(err) {
				t.Fatalf("an allow touched telemetry: %v", err)
			}
		}
	})
	outside := filepath.ToSlash(filepath.Join(t.TempDir(), gateSentinel, "notes.txt"))
	for _, tc := range []struct {
		name, agent, tool string
		input             map[string]string
		m                 mode.Mode
		policy            string
		answer            string
		prepare           func(t *testing.T, reg *session.Registry)
		want              telemetry.Gate
	}{
		{name: "outside by person", agent: "claude", tool: "Read", input: map[string]string{"file_path": outside}, m: mode.Edits, answer: gate.Allow,
			want: telemetry.Gate{Kind: "read", RuleClass: "outside", Answer: "allow", By: "person"}},
		{name: "on_unmatched denied by lead", agent: "codex", tool: "Bash", input: map[string]string{"command": "custom-command --token=" + gateSentinel}, m: mode.Ask, answer: gate.Deny,
			want: telemetry.Gate{Kind: "exec", RuleClass: "on_unmatched", Answer: "deny", By: "lead"}},
		{name: "mode", agent: "claude", tool: "Write", input: map[string]string{"file_path": gateSentinel + ".go", "content": gateSentinel}, m: mode.Ask, answer: gate.Allow,
			want: telemetry.Gate{Kind: "write", RuleClass: "mode", Answer: "allow", By: "lead"}},
		{name: "mcp", agent: "claude", tool: "mcp__" + gateSentinel + "__fetch", input: map[string]string{"url": gateSentinel}, m: mode.Full, answer: gate.Allow,
			want: telemetry.Gate{Kind: "mcp", RuleClass: "mcp", Answer: "allow", By: "lead"}},
		{name: "unverified times out", agent: "codex", tool: "Bash", input: map[string]string{"command": "echo $(" + gateSentinel + ")"}, m: mode.Edits,
			want: telemetry.Gate{Kind: "exec", RuleClass: "unverified", Answer: "timeout", By: "lead", WaitSecs: 1}},
		{name: "unknown tool", agent: "claude", tool: "Tool" + gateSentinel, input: map[string]string{"x": gateSentinel}, m: mode.Edits, answer: gate.Allow,
			want: telemetry.Gate{Kind: "unknown", RuleClass: "unverified", Answer: "allow", By: "lead"}},
		{name: "explicit ask rule", agent: "codex", tool: "Bash", input: map[string]string{"command": "deploy-site " + gateSentinel}, m: mode.Edits, answer: gate.Allow,
			policy: "  rules:\n    - {action: exec, match: \"deploy-site *\", decision: ask}\n",
			want:   telemetry.Gate{Kind: "exec", RuleClass: "other", Answer: "allow", By: "lead"}},
		{name: "auto while the lead waits", agent: "codex", tool: "Bash", input: map[string]string{"command": "custom-command " + gateSentinel}, m: mode.Ask, answer: gate.Allow,
			prepare: func(t *testing.T, reg *session.Registry) {
				if err := reg.SetWaiting("lead-123456", "foreground", os.Getpid()); err != nil {
					t.Fatal(err)
				}
			},
			want: telemetry.Gate{Kind: "exec", RuleClass: "on_unmatched", Answer: "deny", By: "auto"}},
		// Two paths of one patch are one question and one answer.
		{name: "several actions are one ask", agent: "codex", tool: "apply_patch", m: mode.Ask, answer: gate.Allow,
			input: map[string]string{"patch": "*** Begin Patch\n*** Update File: a.go\n*** Update File: ../" + gateSentinel + ".txt\n*** End Patch"},
			want:  telemetry.Gate{Kind: "write", RuleClass: "outside", Answer: "allow", By: "person"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg, root, telemetryRoot := gateTelemetrySetup(t, tc.m, tc.policy)
			if tc.prepare != nil {
				tc.prepare(t, reg)
			}
			answerGateAsks(t, reg, root, tc.answer)
			out := gateHook(t, tc.agent, tc.tool, tc.input, root)
			wantOut := `"permissionDecision":"deny"`
			if tc.want.Answer == "allow" {
				wantOut = ""
			}
			if wantOut == "" && out != "" || wantOut != "" && !strings.Contains(out, wantOut) {
				t.Fatalf("hook output: %s", out)
			}
			events := gateEvents(t, root)
			if len(events) != 1 {
				t.Fatalf("events: %+v", events)
			}
			g := events[0]
			g.Common = telemetry.Common{}
			// The wait is whole seconds: a timeout waits the configured second,
			// an answer comes within the reply poll.
			if g.WaitSecs < tc.want.WaitSecs || g.WaitSecs > tc.want.WaitSecs+1 {
				t.Fatalf("wait_secs %d, want about %d", g.WaitSecs, tc.want.WaitSecs)
			}
			g.WaitSecs = tc.want.WaitSecs
			if g != tc.want {
				t.Fatalf("event %+v, want %+v", g, tc.want)
			}
			if events[0].Type != "gate" || events[0].T == 0 {
				t.Fatalf("header: %+v", events[0].Common)
			}
			// No target, command, path, lead id, rule text or error text persists.
			text := telemetryText(t, telemetryRoot)
			for _, leak := range []string{gateSentinel, root, filepath.ToSlash(root), "lead-123456", "lead.lead-1", "ask lead", "timeout or", "policy", "requires", "\"rule\""} {
				if strings.Contains(text, leak) {
					t.Fatalf("%q reached telemetry: %s", leak, text)
				}
			}
		})
	}
	t.Run("hook reentry", func(t *testing.T) {
		reg, root, _ := gateTelemetrySetup(t, mode.Ask, "")
		answerGateAsks(t, reg, root, gate.Allow)
		a := gate.Action{Kind: "exec", Target: "custom-command", Role: "implement"}
		initial := gate.Decision{Kind: gate.Ask, Rule: "on_unmatched", Mode: mode.Ask}
		cfg, err := configfile.Load(root)
		if err != nil {
			t.Fatal(err)
		}
		b, err := bus.Open(reg.Dir, cfg.Wire)
		if err != nil {
			t.Fatal(err)
		}
		// One hook call: its observer records once, however often Escalate
		// runs again in it.
		s := openTelemetry(cfg, root)
		defer s.Close()
		ctx := gate.WithAskObserver(t.Context(), func(r gate.AskResult) { recordGate(s, a, initial, r) })
		for range 2 {
			if d := gate.Escalate(ctx, b, "lead-123456", a, time.Second, false); d.Kind != gate.Allow {
				t.Fatal(d)
			}
		}
		if events := gateEvents(t, root); len(events) != 1 {
			t.Fatalf("reentry duplicated the ask: %+v", events)
		}
	})
}

// A worker's gate events carry the run id its delegation drew and passed as
// DUOLOG_RUN; a value that is not a duolog id is left out.
func TestTelemetryGateEventsCarryRun(t *testing.T) {
	const run = "0123456789abcdef0123456789abcdef"
	for _, tc := range []struct{ env, want string }{{run, `"` + run + `"`}, {"vendor-session-" + gateSentinel, ""}, {"", ""}} {
		reg, root, telemetryRoot := gateTelemetrySetup(t, mode.Ask, "")
		t.Setenv("DUOLOG_RUN", tc.env)
		answerGateAsks(t, reg, root, gate.Allow)
		gateHook(t, "codex", "Bash", map[string]string{"command": "custom-command"}, root)
		events := gateEvents(t, root)
		if len(events) != 1 {
			t.Fatalf("events: %+v", events)
		}
		got := ""
		if events[0].Run != (telemetry.ID{}) {
			data, err := json.Marshal(events[0].Run)
			if err != nil {
				t.Fatal(err)
			}
			got = string(data)
		}
		if got != tc.want || strings.Contains(telemetryText(t, telemetryRoot), gateSentinel) {
			t.Fatalf("DUOLOG_RUN=%q: run %s", tc.env, got)
		}
	}
}

// Run events take the delegation's run id, so they join its workers' gate
// events; an attempt without a valid one gets a fresh id.
func TestTelemetryRunEventsUseDelegationRun(t *testing.T) {
	const run = "fedcba9876543210fedcba9876543210"
	_, root, _ := gateTelemetrySetup(t, mode.Ask, "")
	t.Setenv("DUOLOG_DEPTH", "")
	cfg, err := configfile.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	s := openTelemetry(cfg, root)
	if s == nil {
		t.Fatal("no store")
	}
	defer s.Close()
	observe := observeAttempt(s, cfg)
	observe(record.Attempt{Number: 1, Run: run, Role: "implement", Agent: "claude", Outcome: "done"})
	observe(record.Attempt{Number: 1, Run: "worker-id", Role: "implement", Agent: "claude", Outcome: "done"})
	got := runs(t, root)
	if len(got) != 2 {
		t.Fatalf("runs: %+v", got)
	}
	first, _ := json.Marshal(got[0].Run)
	second, _ := json.Marshal(got[1].Run)
	if string(first) != `"`+run+`"` || got[1].Run == (telemetry.ID{}) || string(second) == string(first) || strings.Contains(string(second), "worker") {
		t.Fatalf("run ids %s %s", first, second)
	}
}

// A telemetry sink that cannot be used changes no permission outcome: the
// hook prints and logs what it does with collection off.
func TestTelemetryGateFailure(t *testing.T) {
	type outcome struct{ out, decisions string }
	run := func(t *testing.T, answer string, sink func(t *testing.T, telemetryRoot string)) outcome {
		reg, root, telemetryRoot := gateTelemetrySetup(t, mode.Ask, "")
		sink(t, telemetryRoot)
		if answer == "" {
			shortAskWait(t)
		}
		answerGateAsks(t, reg, root, answer)
		out := gateHook(t, "codex", "Bash", map[string]string{"command": "custom-command"}, root)
		decisions, err := os.ReadFile(filepath.Join(root, ".duolog", "decisions.ndjson"))
		if err != nil {
			t.Fatal(err)
		}
		// The reason names the ask's number, which differs per bus.
		var r gate.Record
		if err := json.Unmarshal(decisions, &r); err != nil {
			t.Fatal(err)
		}
		return outcome{out: strings.Split(out, " for ask ")[0], decisions: r.Kind + " " + r.Rule + " " + r.Action.Target}
	}
	off := func(t *testing.T, _ string) { t.Setenv("DUOLOG_TELEMETRY", "off") }
	sinks := map[string]func(t *testing.T, telemetryRoot string){
		// A file where the root belongs: the store cannot open.
		"root is a file": func(t *testing.T, telemetryRoot string) {
			if err := os.MkdirAll(filepath.Dir(telemetryRoot), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(telemetryRoot, []byte(gateSentinel), 0600); err != nil {
				t.Fatal(err)
			}
		},
		// The store opens, but a folder stands where today's segment belongs:
		// the append fails.
		"segment unwritable": func(t *testing.T, telemetryRoot string) {
			root, err := projectRoot()
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := configfile.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			s := openTelemetry(cfg, root)
			if s == nil {
				t.Fatal("telemetry did not open")
			}
			_ = s.Close()
			data, err := os.ReadFile(filepath.Join(telemetryRoot, "salt"))
			if err != nil || len(data) != 32 {
				t.Fatal(len(data), err)
			}
			id := telemetry.ProjectID([32]byte(data), telemetryOptions(cfg, root).CanonicalStateRoot)
			for _, day := range []time.Time{time.Now(), time.Now().Add(time.Hour)} {
				if err := os.MkdirAll(filepath.Join(telemetryRoot, "v1", id, day.UTC().Format(time.DateOnly)+".ndjson"), 0700); err != nil {
					t.Fatal(err)
				}
			}
		},
	}
	for _, answer := range []string{gate.Allow, gate.Deny, ""} {
		want := run(t, answer, off)
		for name, sink := range sinks {
			t.Run(name+"/"+answer, func(t *testing.T) {
				before := (*telemetry.Store)(nil).Health().Failures
				if got := run(t, answer, sink); got != want {
					t.Fatalf("sink failure changed the outcome: %+v, want %+v", got, want)
				}
				// The open that failed records nothing; the append that failed
				// is counted.
				if after := (*telemetry.Store)(nil).Health().Failures; name == "segment unwritable" && after == before {
					t.Fatal("the append did not fail")
				}
			})
		}
	}
	// recordGate itself takes a store that is missing or closed.
	closed, err := telemetry.Open(telemetry.Options{Location: telemetryLocation()})
	if err != nil {
		t.Fatal(err)
	}
	_ = closed.Close()
	for _, s := range []*telemetry.Store{nil, closed} {
		recordGate(s, gate.Action{Kind: "exec", Target: gateSentinel}, gate.Decision{Kind: gate.Ask, Rule: "on_unmatched"}, gate.AskResult{Answer: "allow", By: "lead", Wait: time.Second})
	}
}
