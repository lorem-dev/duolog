package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/telemetry"
)

// leadToolInput is a lead's hook input for one tool call.
func leadToolInput(agent, root, tool string, input map[string]string) []byte {
	data, _ := json.Marshal(map[string]any{"session_id": "lead-" + agent, "cwd": root, "tool_name": tool, "tool_input": input, "permission_mode": "bypassPermissions"})
	return data
}

// A lead model reaches telemetry and balance only through the person: a
// Claude lead's call asks the person, a Codex lead's call is denied with a
// pointer to the person's terminal. Commands, wrappers and reads of either
// root are covered, in every mode; the person's own terminal is not.
func TestTelemetryLeadAccess(t *testing.T) {
	root, telemetryRoot := telemetryCommandSetup(t)
	balanceRoot, err := telemetry.BalanceRoot(telemetryLocation())
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(root, ".duolog")
	type call struct {
		tool  string
		input map[string]string
	}
	shell := func(command string) call { return call{"Bash", map[string]string{"command": command}} }
	// The macOS roots hold a space.
	quote := func(p string) string { return "'" + p + "'" }
	guarded := []call{
		shell("duolog telemetry status"), shell("duolog telemetry show --json"), shell("duolog telemetry prune"),
		shell("duolog telemetry purge --all --yes"), shell("duolog telemetry on"), shell("duolog telemetry off"),
		shell("duolog balance reset"), shell("env DUOLOG_TELEMETRY=off duolog telemetry prune"),
		shell("sh -c 'duolog telemetry show'"), shell("scripts/dev/bin/duolog telemetry status"),
		shell("true && duolog.exe telemetry purge --yes; echo done"), shell(`"duolog" --lead L telemetry show`),
		shell("cat " + quote(filepath.Join(telemetryRoot, "salt"))), shell("head " + quote(filepath.Join(telemetryRoot, "v1", "_account", "2026-11-01.ndjson"))),
		shell("grep -r claude " + quote(filepath.Dir(telemetryRoot))), shell("ls " + quote(balanceRoot)),
		shell("cat " + quote(filepath.Join(balanceRoot, "samples.ndjson"))),
		{"Read", map[string]string{"file_path": filepath.Join(telemetryRoot, "salt")}},
		{"Read", map[string]string{"file_path": filepath.Join(balanceRoot, "state.json")}},
		{"Grep", map[string]string{"pattern": "claude", "path": telemetryRoot}},
		{"Grep", map[string]string{"pattern": "claude", "path": balanceRoot}},
	}
	open := []call{
		shell("duolog doctor"), shell("go test ./..."), shell("cat notes.txt"), shell("duolog delegate implement --task telemetry.md"),
		{"Read", map[string]string{"file_path": filepath.Join(root, "README.md")}},
		{"Grep", map[string]string{"pattern": "telemetry", "path": root}},
	}
	for _, agent := range []string{"claude", "codex"} {
		for _, c := range guarded {
			// A Codex lead reads through its shell; it has no Read or Grep tool.
			if agent == "codex" && c.tool != "Bash" {
				continue
			}
			input := leadToolInput(agent, root, c.tool, c.input)
			d, ok := leadGuard(stateDir, agent, input)
			if !ok {
				t.Errorf("%s %s %v passed the lead guard", agent, c.tool, c.input)
				continue
			}
			switch agent {
			case "claude":
				if d.Kind != gate.Ask || !strings.Contains(d.Reason, "the person") {
					t.Errorf("claude %v: %+v", c.input, d)
				}
			case "codex":
				if d.Kind != gate.Deny || !strings.Contains(d.Reason, "run it in your terminal") {
					t.Errorf("codex %v: %+v", c.input, d)
				}
			}
		}
		for _, c := range open {
			if d, ok := leadGuard(stateDir, agent, leadToolInput(agent, root, c.tool, c.input)); ok {
				t.Errorf("%s %v guarded: %+v", agent, c.input, d)
			}
		}
	}

	// telemetryLeadGuard takes the actions as the hook normalised them.
	if d, ok := telemetryLeadGuard("codex", []gate.Action{{Kind: "exec", Target: "duolog telemetry prune", Work: root, Cwd: root}}); !ok || d.Kind != gate.Deny {
		t.Fatalf("%+v %v", d, ok)
	}
	if _, ok := telemetryLeadGuard("claude", []gate.Action{{Kind: "exec", Target: "go vet ./...", Work: root, Cwd: root}}); ok {
		t.Fatal("an ordinary command was guarded")
	}

	// A lead in full mode is asked as well: the installed lead hook answers.
	reg, err := session.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "codex"} {
		if err := reg.RegisterLead(session.Lead{ID: "lead-" + agent, Target: config.Target{Agent: agent}, Mode: mode.Full}); err != nil {
			t.Fatal(err)
		}
		input := leadToolInput(agent, root, "Bash", map[string]string{"command": "duolog telemetry show"})
		out, err := gateCommand(t, string(input), "gate", "--agent", agent, "--event", "PreToolUse")
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"claude": `"permissionDecision":"ask"`, "codex": `"permissionDecision":"deny"`}[agent]
		if !strings.Contains(out, want) {
			t.Fatalf("%s full mode: %s", agent, out)
		}
	}

	// The person's own terminal runs the commands with no hook between.
	asTerminal(t, true)
	if out, _, err := telemetryCommand(t, "", "status"); err != nil || !strings.Contains(out, "telemetry: on\n") {
		t.Fatalf("terminal status: %v\n%s", err, out)
	}
}
