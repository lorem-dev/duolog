package delegate

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/gate"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/mode"
	corepolicy "github.com/lorem-dev/duolog/internal/core/policy"
)

// workerModes writes a user-level worker_modes section and reloads the config.
func (h *harness) workerModes(yaml string) {
	h.t.Helper()
	userDir, _ := os.UserConfigDir()
	put(h.t, filepath.Join(userDir, "duolog", "config.yaml"), "worker_modes:\n"+yaml)
	var err error
	if h.cfg, err = configfile.Load(h.root); err != nil {
		h.t.Fatal(err)
	}
}

// leadNative registers the lead and records its native mode name.
func (h *harness) leadNative(agent string, m mode.Mode, native string) {
	h.t.Helper()
	h.lead(agent, m, "high")
	h.observe(session.LeadObservation{ID: "lead-123456", Agent: agent, Mode: &m, Native: &native})
}

// claudeGate installs the duolog gate in the project's local Claude settings,
// or removes it.
func (h *harness) claudeGate(on bool) {
	h.t.Helper()
	path := filepath.Join(h.root, ".claude", "settings.local.json")
	if !on {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			h.t.Fatal(err)
		}
		return
	}
	put(h.t, path, `{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"duolog gate --agent claude --event PreToolUse"}]}]}}`)
}

// hooksOff turns every Claude hook off in the project's shared settings.
func (h *harness) hooksOff() {
	h.t.Helper()
	put(h.t, filepath.Join(h.root, ".claude", "settings.json"), `{"disableAllHooks": true}`)
}

// gateOnPath replaces the harness's GateBinary.
func (h *harness) gateOnPath(ok bool) {
	h.t.Helper()
	h.deps.GateBinary = func(string) (string, error) {
		if ok {
			return filepath.Join(h.root, "bin", "duolog"), nil
		}
		return "", errors.New("duolog is not on the worker PATH")
	}
}

// codexHooksOnly approves the Codex hooks but leaves the folder untrusted.
func (h *harness) codexHooksOnly() {
	h.t.Helper()
	put(h.t, filepath.Join(h.root, ".codex", "hooks.json"), codexHooks(h.t))
	s := fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"fixture-hash\"\n", filepath.Join(h.root, ".codex", "hooks.json")+":PreToolUse:0:0")
	put(h.t, filepath.Join(os.Getenv("HOME"), ".codex", "config.toml"), s)
}

// codexHooks is the Codex hooks.json duolog installs, so that trust can show
// the approved gate is duolog's own command. testdata/codex-hooks.json holds
// it; TestDelegateHooksFixtureMatchesInstall in cmd/duolog keeps it equal to
// what internal/install writes.
func codexHooks(t *testing.T) string {
	t.Helper()
	_, testFile, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(testFile), "testdata", "codex-hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// runOn runs role on agent/model at effort medium, as --use does.
func (h *harness) runOn(role, agent, model string) (Outcome, error) {
	h.t.Helper()
	opts := Options{Entry: &config.Target{Agent: agent, Model: model, Effort: "medium"}, Person: true}
	return h.runWith(role, opts)
}

func TestNoWorkerModesKeepsToday(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Edits, "high")
	out, err := h.run("implement") // codex sol under a claude lead
	if err != nil || h.calls[0].Native != "workspace-write" || h.calls[0].Network || strings.Contains(out.Line, "native=") {
		t.Fatal(out, err, h.calls[0].Native)
	}
	// The admitted process: the account gate built it with these variables.
	env := strings.Join(h.calls[0].Process.Env, "\n")
	if strings.Contains(env, "DUOLOG_MODE=") || !strings.Contains(env, "DUOLOG_NATIVE=") {
		t.Fatal(env)
	}
}

func TestWorkerModeNeverAboveLead(t *testing.T) {
	for _, tc := range []struct {
		lead      mode.Mode
		native    string
		limit     mode.Mode // "" for no duolog mode
		entry     string
		want      string
		wantLevel mode.Mode
	}{
		{mode.Edits, "acceptEdits", "", "claude: auto", "acceptEdits", mode.Edits},
		{mode.Edits, "auto", "", "claude: auto", "auto", mode.Edits},
		{mode.Edits, "acceptEdits", "", "claude: bypassPermissions", "acceptEdits", mode.Edits},
		{mode.Full, "bypassPermissions", "", "claude: bypassPermissions", "bypassPermissions", mode.Full},
		{mode.Full, "bypassPermissions", mode.Ask, "claude: bypassPermissions", "default", mode.Ask},
	} {
		h := newHarness(t)
		h.claudeGate(true)
		h.gateOnPath(true)
		h.workerModes("  roles:\n    implement: {" + tc.entry + "}\n")
		h.leadNative("claude", tc.lead, tc.native)
		if tc.limit != "" {
			if err := h.reg.SetMode("lead-123456", tc.limit); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := h.runOn("implement", "claude", "sonnet"); err != nil {
			t.Fatal(tc, err)
		}
		if c := h.calls[0]; c.Native != tc.want || c.Mode != tc.wantLevel {
			t.Errorf("%+v: native %s mode %s", tc, c.Native, c.Mode)
		}
	}
}

// With no worker_modes entry, a Claude worker starts in auto when the lead's
// ceiling allows it (a lead at full, or a Claude lead in auto at edits) and in
// acceptEdits otherwise; a project entry may lower it, and bypassPermissions is
// never the default.
func TestClaudeWorkerStartsInAutoUnderFullLead(t *testing.T) {
	for _, tc := range []struct {
		name    string
		lead    mode.Mode
		native  string
		limit   mode.Mode // "" for no duolog mode
		entry   string    // worker_modes roles.implement
		want    string
		wantArg string
	}{
		{"full lead", mode.Full, "bypassPermissions", "", "", "auto", "auto"},
		{"claude auto lead at edits", mode.Edits, "auto", "", "", "auto", "auto"},
		{"edits lead", mode.Edits, "acceptEdits", "", "", "acceptEdits", "acceptEdits"},
		{"DUOLOG_MODE edits under a full lead", mode.Full, "bypassPermissions", mode.Edits, "", "acceptEdits", "acceptEdits"},
		{"entry lowers to acceptEdits", mode.Full, "bypassPermissions", "", "claude: acceptEdits", "acceptEdits", "acceptEdits"},
	} {
		h := newHarness(t)
		h.claudeGate(true)
		h.gateOnPath(true)
		if tc.entry != "" {
			h.workerModes("  roles:\n    implement: {" + tc.entry + "}\n")
		}
		h.leadNative("claude", tc.lead, tc.native)
		if tc.limit != "" {
			if err := h.reg.SetMode("lead-123456", tc.limit); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := h.runOn("implement", "claude", "sonnet"); err != nil {
			t.Fatal(tc.name, err)
		}
		c := h.calls[0]
		if c.Native != tc.want || !slices.Equal(c.Process.Launch, []string{"-p", "--permission-mode", tc.wantArg}) {
			t.Errorf("%s: native %s, launch %v", tc.name, c.Native, c.Process.Launch)
		}
		if env := strings.Join(c.Process.Env, "\n"); !strings.Contains(env, "DUOLOG_NATIVE="+tc.want) {
			t.Errorf("%s: env lacks DUOLOG_NATIVE=%s", tc.name, tc.want)
		}
	}
}

func TestFamilyEntryLowersToPlan(t *testing.T) {
	h := newHarness(t)
	h.workerModes("  families:\n    luna: {codex: read-only}\n")
	h.lead("codex", mode.Full, "high")
	if _, err := h.run("mechanical"); err != nil { // mechanical uses codex luna
		t.Fatal(err)
	}
	if c := h.calls[0]; c.Native != "read-only" || c.Mode != mode.Plan || !strings.Contains(strings.Join(c.Process.Env, "\n"), "DUOLOG_MODE=plan") {
		t.Fatal(c.Native, c.Mode)
	}
}

func TestCodexExtrasOnlyForTheRole(t *testing.T) {
	h := newHarness(t)
	h.gateOnPath(true)
	h.workerModes("  roles:\n    implement: {codex: workspace-write, network: true, writable_roots: [\"" + filepath.ToSlash(t.TempDir()) + "\"]}\n")
	h.lead("codex", mode.Edits, "high")
	for role, want := range map[string]bool{"implement": true, "tests": false} {
		h.calls = nil
		if _, err := h.run(role); err != nil {
			t.Fatal(role, err)
		}
		if c := h.calls[0]; c.Network != want || (len(c.WritableRoots) > 0) != want {
			t.Errorf("%s: network %t roots %v", role, c.Network, c.WritableRoots)
		}
	}
	h.calls = nil
	h.lead("codex", mode.Ask, "high")
	if _, err := h.run("implement"); err != nil || h.calls[0].Network || len(h.calls[0].WritableRoots) > 0 {
		t.Fatal("extras under an ask lead", err)
	}
	h.calls = nil
	h.lead("codex", mode.Edits, "high")
	h.unavailable("codex") // falls back to claude sonnet
	if _, err := h.run("implement"); err != nil || h.calls[0].Route.Agent != "claude" || h.calls[0].Network || len(h.calls[0].WritableRoots) > 0 {
		t.Fatal("claude fallback got codex extras", err)
	}
}

func TestFailedGatePreflightRefusesRun(t *testing.T) {
	for _, tc := range []struct {
		name, agent, model string
		setup              func(h *harness)
		entry              string
	}{
		{"claude gate missing", "claude", "sonnet", func(h *harness) { h.claudeGate(false); h.gateOnPath(true) }, "claude: bypassPermissions"},
		{"claude hooks off", "claude", "sonnet", func(h *harness) { h.claudeGate(true); h.hooksOff(); h.gateOnPath(true) }, "claude: bypassPermissions"},
		{"binary missing", "claude", "sonnet", func(h *harness) { h.claudeGate(true); h.gateOnPath(false) }, "claude: bypassPermissions"},
		// Hooks approved, folder not trusted: trustWarning only warns, so the
		// preflight is what refuses.
		{"codex folder untrusted", "codex", "sol", func(h *harness) { h.codexHooksOnly(); h.gateOnPath(true) }, "codex: workspace-write, network: true"},
		{"protected root", "codex", "sol", func(h *harness) { h.gateOnPath(true) }, "codex: workspace-write, writable_roots: [\"<state>\"]"},
		// Codex review 6: an approval of a gate command that changed since, and
		// a Claude plugin that is enabled but not installed.
		{"codex hook changed since approval", "codex", "sol", func(h *harness) {
			h.gateOnPath(true)
			put(h.t, filepath.Join(h.root, ".codex", "hooks.json"), strings.Replace(codexHooks(h.t), "--event PreToolUse", "--event PreToolUse --agent claude", 2))
		}, "codex: workspace-write, network: true"},
		{"claude plugin not installed", "claude", "sonnet", func(h *harness) {
			h.claudeGate(false)
			h.gateOnPath(true)
			put(h.t, filepath.Join(os.Getenv("HOME"), ".claude", "settings.json"), `{"enabledPlugins":{"duolog@duolog":true}}`)
		}, "claude: bypassPermissions"},
	} {
		h := newHarness(t)
		tc.setup(h)
		h.workerModes("  roles:\n    implement: {" + strings.ReplaceAll(tc.entry, "<state>", filepath.ToSlash(filepath.Join(h.root, ".duolog"))) + "}\n")
		h.leadNative("claude", mode.Full, "bypassPermissions")
		out, err := h.runOn("implement", tc.agent, tc.model)
		t.Logf("%s: %s", tc.name, out.Line)
		h.requireCalls(0) // neither the worker nor a fallback agent ran
		var notStarted *NotStartedError
		if err == nil || !errors.As(err, &notStarted) || !strings.Contains(out.Line, "gate preflight failed") || !strings.Contains(out.Line, "nothing ran") {
			t.Errorf("%s: %v %q", tc.name, err, out.Line)
		}
	}
	// No entry and the same broken Claude gate: today's behaviour, a warning only.
	h := newHarness(t)
	h.claudeGate(false)
	h.gateOnPath(false)
	h.lead("claude", mode.Edits, "high")
	if _, err := h.runOn("implement", "claude", "sonnet"); err != nil {
		t.Fatal(err)
	}
	h.requireCalls(1)
}

// Claude review I3, Codex review 5: extras on a role entry apply when the
// resolved Codex sandbox and level allow them, whichever entry named the
// native mode.
func TestExtrasOnlyRoleEntryApplies(t *testing.T) {
	for _, entry := range []string{"network: true, writable_roots: [\"<dir>\"]", "claude: auto, network: true, writable_roots: [\"<dir>\"]"} {
		h := newHarness(t)
		h.gateOnPath(true)
		h.workerModes("  roles:\n    implement: {" + strings.ReplaceAll(entry, "<dir>", filepath.ToSlash(t.TempDir())) + "}\n")
		h.lead("codex", mode.Edits, "high")
		out, err := h.run("implement")
		if err != nil {
			t.Fatal(entry, err)
		}
		if c := h.calls[0]; c.Native != "workspace-write" || !c.Network || len(c.WritableRoots) != 1 || !strings.Contains(out.Line, "native=workspace-write+network+roots=1 (role implement)") {
			t.Errorf("%s: native %s network %t roots %v line %q", entry, c.Native, c.Network, c.WritableRoots, out.Line)
		}
	}
	// A family entry that lowers Codex to read-only still drops them.
	h := newHarness(t)
	h.gateOnPath(true)
	h.workerModes("  roles:\n    implement: {network: true}\n  families:\n    sol: {codex: read-only}\n")
	h.lead("codex", mode.Edits, "high")
	if _, err := h.run("implement"); err != nil || h.calls[0].Native != "read-only" || h.calls[0].Network {
		t.Fatal(err, h.calls[0].Native, h.calls[0].Network)
	}
}

// Claude review I2: without an entry the gate keeps the lead's mode, so a
// read-only role reads the web as before; an entry sets DUOLOG_MODE.
func TestDuologModeOnlyWithAnEntry(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Edits, "high")
	for _, role := range []string{"review_final", "implement"} {
		h.calls = nil
		if _, err := h.run(role); err != nil {
			t.Fatal(role, err)
		}
		if env := strings.Join(h.calls[0].Process.Env, "\n"); strings.Contains(env, "DUOLOG_MODE=") || !strings.Contains(env, "DUOLOG_NATIVE=") {
			t.Errorf("%s: %s", role, env)
		}
	}
	h.workerModes("  roles:\n    review_final: {claude: plan, codex: read-only}\n")
	h.calls = nil
	if _, err := h.run("review_final"); err != nil || !strings.Contains(strings.Join(h.calls[0].Process.Env, "\n"), "DUOLOG_MODE=plan") {
		t.Fatal(err, h.calls[0].Process.Env)
	}
}

// A read-only role's Claude worker, under any lead or with an entry asking
// plan, and a write role whose entry asks plan, all start in Claude's
// default mode, never in plan; the level stays plan, so the gate keeps the
// worker read-only.
func TestClaudeWorkerNeverLaunchesInPlan(t *testing.T) {
	for _, tc := range []struct {
		name, role, entry string
		lead              mode.Mode
	}{
		{"read-only role", "review_final", "", mode.Full},
		{"lead in plan", "review_final", "", mode.Plan},
		{"read-only role entry asks plan", "review_final", "  roles:\n    review_final: {claude: plan}\n", mode.Full},
		{"write role entry asks plan", "implement", "  roles:\n    implement: {claude: plan}\n", mode.Full},
	} {
		h := newHarness(t)
		if tc.entry != "" {
			h.workerModes(tc.entry)
		}
		h.lead("claude", tc.lead, "high")
		if _, err := h.runOn(tc.role, "claude", "haiku"); err != nil {
			t.Fatal(tc.name, err)
		}
		c := h.calls[0]
		launch := strings.Join(c.Process.Launch, " ")
		if c.Native != "default" || c.Mode != mode.Plan || launch != "-p --permission-mode default" || strings.Contains(c.Process.Settings, `"plan"`) {
			t.Errorf("%s: native %s mode %s launch %q", tc.name, c.Native, c.Mode, launch)
		}
		if slices.Contains(c.Process.Env, "DUOLOG_NATIVE=plan") || !slices.Contains(c.Process.Env, "DUOLOG_NATIVE=default") {
			t.Errorf("%s: the gate's log would not name native default", tc.name)
		}
	}
}

// Codex review 2: a Claude worker at level plan or in a read-only role starts
// in Claude's default mode, so only duolog's gate keeps it read-only. It
// starts only after a gate preflight in its own environment, though it asks
// nothing looser than duolog's default. Hooks that are missing, or turned off
// while the settings allow Write, Edit and Bash, start no process at all.
func TestGateHeldClaudeWorkerNeedsItsGate(t *testing.T) {
	inherited := `{"disableAllHooks":true,"permissions":{"allow":["Write","Edit","Bash(*)"]}}`
	for _, tc := range []struct {
		name, role, entry string
		lead              mode.Mode
		setup             func(h *harness)
	}{
		{"read-only role, hooks missing", "review_final", "", mode.Full, func(h *harness) { h.claudeGate(false); h.gateOnPath(true) }},
		{"read-only role, hooks off with inherited allows", "review_final", "", mode.Full, func(h *harness) {
			h.claudeGate(true)
			h.gateOnPath(true)
			put(h.t, filepath.Join(h.root, ".claude", "settings.json"), inherited)
		}},
		{"user settings turn hooks off", "review_final", "", mode.Edits, func(h *harness) {
			h.claudeGate(true)
			h.gateOnPath(true)
			put(h.t, filepath.Join(os.Getenv("HOME"), ".claude", "settings.json"), inherited)
		}},
		{"lead in plan, hooks off", "review_final", "", mode.Plan, func(h *harness) { h.claudeGate(true); h.hooksOff(); h.gateOnPath(true) }},
		{"entry asks plan, hooks missing", "implement", "  roles:\n    implement: {claude: plan}\n", mode.Full, func(h *harness) { h.claudeGate(false); h.gateOnPath(true) }},
		{"gate command not on the worker PATH", "review_final", "", mode.Full, func(h *harness) { h.claudeGate(true); h.gateOnPath(false) }},
	} {
		h := newHarness(t)
		tc.setup(h)
		if tc.entry != "" {
			h.workerModes(tc.entry)
		}
		h.lead("claude", tc.lead, "high")
		out, err := h.runOn(tc.role, "claude", "haiku")
		h.requireCalls(0) // neither the worker nor a fallback agent ran
		var notStarted *NotStartedError
		if err == nil || !errors.As(err, &notStarted) || !strings.Contains(out.Line, "gate preflight failed") || !strings.Contains(out.Line, "nothing ran") {
			t.Errorf("%s: %v %q", tc.name, err, out.Line)
		}
	}
	// With its gate shown, the same worker runs.
	h := newHarness(t)
	h.claudeGate(true)
	h.gateOnPath(true)
	h.lead("claude", mode.Full, "high")
	if out, err := h.runOn("review_final", "claude", "haiku"); err != nil {
		t.Fatal(out.Line, err)
	}
	h.requireCalls(1)
}

// unprovenGates are Claude settings whose PreToolUse hook names the gate but
// cannot be shown to run it: the gate's words in a shell comment, and an
// absolute path to a duolog that does not exist.
func unprovenGates(t *testing.T) map[string]string {
	missing, _ := json.Marshal(filepath.Join(t.TempDir(), "gone", "duolog"))
	return map[string]string{
		"comment":            `{"permissions":{"allow":["Write","Edit","Bash(*)"]},"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"true # duolog gate --agent claude --event PreToolUse"}]}]}}`,
		"missing executable": `{"permissions":{"allow":["Write","Edit","Bash(*)"]},"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":` + string(missing) + `,"args":["gate","--agent","claude","--event","PreToolUse"]}]}]}}`,
	}
}

// Re-review 3: a held Claude worker starts only when its hook is the gate
// command duolog install writes, run by the duolog the launch uses. A hook
// that only names the gate, or runs a duolog that is not there, starts
// nothing, and the message names the hook and duolog install.
func TestHeldWorkerRefusesAnUnprovenGateHook(t *testing.T) {
	for name, settings := range unprovenGates(t) {
		h := newHarness(t)
		h.gateOnPath(true)
		put(h.t, filepath.Join(h.root, ".claude", "settings.local.json"), settings)
		h.lead("claude", mode.Full, "high")
		out, err := h.runOn("review_final", "claude", "haiku")
		h.requireCalls(0)
		if err == nil || !strings.Contains(out.Line, "gate preflight failed") || !strings.Contains(out.Line, "duolog install") || !strings.Contains(out.Line, "settings.local.json") {
			t.Errorf("%s: %v %q", name, err, out.Line)
		}
	}
	// The command install writes, as one string or as program and args, runs.
	for _, settings := range []string{
		`{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"duolog gate --agent claude --event PreToolUse"}]}]}}`,
		`{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"duolog","args":["gate","--agent","claude","--event","PreToolUse"]}]}]}}`,
	} {
		h := newHarness(t)
		h.gateOnPath(true)
		put(h.t, filepath.Join(h.root, ".claude", "settings.local.json"), settings)
		h.lead("claude", mode.Full, "high")
		if out, err := h.runOn("review_final", "claude", "haiku"); err != nil {
			t.Fatal(settings, out.Line, err)
		}
	}
}

// devGateHook is the PreToolUse gate of duolog's own .claude/settings.json,
// which runs the dev launcher of the checkout.
const devGateHook = `exec "$CLAUDE_PROJECT_DIR"/scripts/dev/bin/duolog gate --agent claude --event PreToolUse`

// devGateSettings is Claude settings whose only PreToolUse hook is command.
func devGateSettings(t *testing.T, command string) string {
	t.Helper()
	data, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	return `{"permissions":{"allow":["Write","Edit","Bash(*)"]},"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":` + string(data) + `}]}]}}`
}

// Re-review 5, R-1: in duolog's own checkout the held Claude worker starts
// with the dev hook, the variable read as the project root, when the file it
// names is the dev launcher, though the duolog on the worker PATH is the
// build. Any other shell text, or a file there that is not the launcher,
// starts nothing.
func TestHeldWorkerAcceptsTheDevLauncherHook(t *testing.T) {
	_, testFile, _, _ := runtime.Caller(0)
	launcher := read(t, filepath.Join(filepath.Dir(testFile), "..", "..", "..", "scripts", "dev", "bin", "duolog"))
	run := func(settings, file string) (Outcome, error) {
		h := newHarness(t)
		h.gateOnPath(true)
		if file != "" {
			put(t, filepath.Join(h.root, "scripts", "dev", "bin", "duolog"), file)
		}
		put(t, filepath.Join(h.root, ".claude", "settings.json"), settings)
		h.lead("claude", mode.Full, "high")
		out, err := h.runOn("review_final", "claude", "haiku")
		if err == nil {
			h.requireCalls(1)
		} else {
			h.requireCalls(0)
		}
		return out, err
	}
	// A Windows checkout may give the launcher either line ending.
	lf := strings.ReplaceAll(launcher, "\r\n", "\n")
	for _, file := range []string{lf, strings.ReplaceAll(lf, "\n", "\r\n")} {
		if out, err := run(devGateSettings(t, devGateHook), file); err != nil {
			t.Fatal(out.Line, err)
		}
	}
	for name, c := range map[string]struct{ command, file string }{
		"no launcher":            {devGateHook, ""},
		"another script there":   {devGateHook, "#!/bin/sh\nexit 0\n"},
		"text after the gate":    {devGateHook + "; true", launcher},
		"comment after the gate": {devGateHook + " # x", launcher},
		"another variable":       {`exec "$HOME"/scripts/dev/bin/duolog gate --agent claude --event PreToolUse`, launcher},
		"no exec":                {`"$CLAUDE_PROJECT_DIR"/scripts/dev/bin/duolog gate --agent claude --event PreToolUse`, launcher},
		"another event":          {`exec "$CLAUDE_PROJECT_DIR"/scripts/dev/bin/duolog gate --agent claude --event PermissionRequest`, launcher},
	} {
		out, err := run(devGateSettings(t, c.command), c.file)
		if err == nil || !strings.Contains(out.Line, "gate preflight failed") || !strings.Contains(out.Line, "duolog install") {
			t.Errorf("%s: %v %q", name, err, out.Line)
		}
	}
}

// Re-review 5, N2: Claude does not wait for an async gate hook, and runs the
// tool when a hook times out, so a held Claude worker starts only when its
// gate hook is neither async nor asyncRewake and has no timeout below the 150
// seconds a lead's ask needs. Without a timeout, Claude's default applies.
func TestHeldWorkerRefusesAnAsyncOrShortGateHook(t *testing.T) {
	hook := func(extra string) string {
		return `{"permissions":{"allow":["Write","Edit","Bash(*)"]},"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"duolog","args":["gate","--agent","claude","--event","PreToolUse"]` + extra + `}]}]}}`
	}
	run := func(settings string) (Outcome, error) {
		h := newHarness(t)
		h.gateOnPath(true)
		put(t, filepath.Join(h.root, ".claude", "settings.local.json"), settings)
		h.lead("claude", mode.Full, "high")
		return h.runOn("review_final", "claude", "haiku")
	}
	for name, extra := range map[string]string{
		"async":          `,"async":true`,
		"asyncRewake":    `,"asyncRewake":true`,
		"one second":     `,"timeout":1`,
		"below 150":      `,"timeout":149.5`,
		"async and long": `,"async":true,"timeout":150`,
	} {
		out, err := run(hook(extra))
		if err == nil || !strings.Contains(out.Line, "gate preflight failed") || !strings.Contains(out.Line, "duolog install") {
			t.Errorf("%s: %v %q", name, err, out.Line)
		}
	}
	for _, extra := range []string{"", `,"timeout":150`, `,"timeout":600`, `,"async":false`} {
		if out, err := run(hook(extra)); err != nil {
			t.Errorf("%q: %v %q", extra, err, out.Line)
		}
	}
}

// Re-review 4: a worker started under a lead in plan keeps level plan when
// the lead moves to acceptEdits while it runs, as a peer does: the gate,
// reading the live lead mode and the worker's environment, still denies its
// write command and its web read.
func TestWorkerKeepsPlanWhenItsLeadLoosens(t *testing.T) {
	h := newHarness(t)
	h.claudeGate(true)
	h.gateOnPath(true)
	h.leadNative("claude", mode.Plan, "plan")
	policy := h.cfg.Policy
	policy.Rules = append([]config.Rule{{Action: "exec", Match: "touch *", Decision: corepolicy.Allow}}, policy.Rules...)
	var during []corepolicy.Decision
	h.beforeStart = func() {
		h.leadNative("claude", mode.Edits, "acceptEdits")
		_, live, err := gate.LeadMode(h.reg, "lead-123456", time.Now())
		if err != nil || live != mode.Edits {
			t.Fatal("the lead did not move:", live, err)
		}
		env := h.calls[len(h.calls)-1].Process.Env
		m := corepolicy.WorkerMode(live, corelaunch.Getenv(env, "DUOLOG_MODE"))
		lead := corepolicy.LeadRules{RoleSandbox: h.cfg.Roles["review_final"].Sandbox}
		for _, a := range []corepolicy.Action{
			{Kind: "exec", Target: "touch marker.txt", Role: "review_final"},
			{Kind: "net", Target: "https://example.com/", Role: "review_final"},
		} {
			during = append(during, corepolicy.Evaluate(policy, lead, m, a))
		}
	}
	if out, err := h.runOn("review_final", "claude", "haiku"); err != nil {
		t.Fatal(out.Line, err)
	}
	if len(during) != 2 {
		t.Fatal("the worker did not run")
	}
	for _, d := range during {
		if d.Kind != corepolicy.Deny || d.Mode != mode.Plan {
			t.Errorf("after the lead loosened: %+v", d)
		}
	}
}

// Re-review m1 and m2: a family entry that asks above duolog's default does
// not lower the level below the default's, and a family entry does not set
// the gate of a read-only role to plan.
func TestFamilyEntryKeepsTheDefaultsLevel(t *testing.T) {
	h := newHarness(t)
	h.claudeGate(true)
	h.gateOnPath(true)
	h.workerModes("  families:\n    opus: {claude: bypassPermissions}\n    haiku: {claude: acceptEdits}\n    luna: {codex: workspace-write}\n")
	h.leadNative("claude", mode.Full, "bypassPermissions")
	for _, tc := range []struct{ role, agent, model string }{
		{"implement", "claude", "opus"},
		{"review_final", "claude", "haiku"},
		{"review_intermediate", "codex", "luna"},
	} {
		h.calls = nil
		out, err := h.runOn(tc.role, tc.agent, tc.model)
		if err != nil {
			t.Fatal(tc, err)
		}
		c := h.calls[0]
		if env := strings.Join(c.Process.Env, "\n"); strings.Contains(env, "DUOLOG_MODE=") || strings.Contains(out.Line, "native=") {
			t.Errorf("%v: mode %s native %s: %s; %s", tc, c.Mode, c.Native, env, out.Line)
		}
	}
	if c := h.calls[0]; c.Mode != mode.Plan {
		t.Errorf("review_intermediate mode %s", c.Mode)
	}
}

// Claude review I1, Codex review 2: at launch the roots are checked again,
// canonically, against the credential folders and the folders that hold the
// gate and the agents.
func TestWritableRootCheckedAgainAtLaunch(t *testing.T) {
	bin := t.TempDir()
	// Windows finds a program on PATH by its PATHEXT suffix only.
	claude := "claude"
	if runtime.GOOS == "windows" {
		claude += ".exe"
	}
	put(t, filepath.Join(bin, claude), "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(bin, claude), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, tc := range []struct {
		name    string
		root    func(h *harness) string
		repoint bool
	}{
		{"symlink moved to ~/.ssh", func(h *harness) string { return symlinkTo(t, t.TempDir()) }, true},
		{"folder holding claude", func(h *harness) string { return bin }, false},
		{"folder holding the gate", func(h *harness) string { return filepath.Join(h.root, "bin") }, false},
	} {
		h := newHarness(t)
		h.gateOnPath(true)
		ssh := filepath.Join(os.Getenv("HOME"), ".ssh")
		if err := os.MkdirAll(ssh, 0o700); err != nil {
			t.Fatal(err)
		}
		root := tc.root(h)
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		h.workerModes("  roles:\n    implement: {codex: workspace-write, writable_roots: [\"" + filepath.ToSlash(root) + "\"]}\n")
		if tc.repoint {
			if err := os.Remove(root); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(ssh, root); err != nil {
				t.Fatal(err)
			}
		}
		h.lead("codex", mode.Edits, "high")
		out, err := h.run("implement")
		if len(h.calls) != 0 || err == nil || !strings.Contains(out.Line, "gate preflight failed") {
			t.Errorf("%s: calls %d, %v %q", tc.name, len(h.calls), err, out.Line)
		}
	}
}

// Re-review Important 2: a project source that moves a role's route (a
// learning ladder step, a version pin, the lead's --use) cannot escape a
// family entry that lowers the role's route as the person configured it.
func TestRouteChangeCannotEscapeFamilyLowering(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model string // the step the project's ladder gives implement
		use   string // the --use model, instead of a ladder
	}{
		{name: "ladder moves to opus", model: "opus"},
		{name: "ladder pins a haiku version", model: "claude-3-5-haiku-20241022"},
		{name: "lead moves it with --use", use: "opus"},
	} {
		h := newHarness(t)
		h.claudeGate(true)
		h.gateOnPath(true)
		userDir, _ := os.UserConfigDir()
		put(t, filepath.Join(userDir, "duolog", "config.yaml"), "roles:\n  implement: {use: {agent: claude, model: haiku, effort: medium}}\nworker_modes:\n  roles:\n    implement: {claude: bypassPermissions}\n  families:\n    haiku: {claude: plan}\n")
		project := "learning:\n  enabled: true\n"
		if tc.model != "" {
			project += "  classes:\n    mine:\n      description: mine\n      roles: [implement]\n      steps:\n        implement: [{agent: claude, model: " + tc.model + ", effort: medium, prior: 3}]\n      ladder: [{agent: claude, model: " + tc.model + ", effort: medium, prior: 3}]\n"
		}
		put(t, filepath.Join(h.s.Dir, "config.yaml"), project)
		var err error
		if h.cfg, err = configfile.Load(h.root); err != nil {
			t.Fatal(tc.name, err)
		}
		h.leadNative("claude", mode.Full, "bypassPermissions")
		var out Outcome
		if tc.use != "" {
			out, err = h.runOn("implement", "claude", tc.use)
		} else {
			out, err = h.runWith("implement", Options{Class: "mine"})
		}
		if err != nil {
			t.Fatal(tc.name, out, err)
		}
		h.requireCalls(1)
		// Level plan, in Claude's default mode: the gate keeps it read-only.
		if c := h.calls[0]; c.Native != "default" || c.Mode != mode.Plan {
			t.Errorf("%s: %s at %s on %s, want default at plan (%s)", tc.name, c.Native, c.Mode, c.Route.Model, out.Line)
		}
	}
}

// Integration review, Critical: the gate preflight reads the settings the
// worker's own environment selects. A CLAUDE_CONFIG_DIR or CODEX_HOME that
// only duolog's caller has, because env_passthrough does not pass it, holds
// a gate the worker never loads, so the looser mode is refused.
func TestGatePreflightReadsTheWorkersSettings(t *testing.T) {
	// The variable that names the worker's home: USERPROFILE on Windows.
	home := "HOME"
	if runtime.GOOS == "windows" {
		home = "USERPROFILE"
	}
	for _, tc := range []struct {
		name, agent, model, entry, lead string
		setup                           func(h *harness, alternate string)
		passthrough                     string
	}{
		{name: "claude gate only in the caller's CLAUDE_CONFIG_DIR", agent: "claude", model: "sonnet", entry: "claude: bypassPermissions", lead: "bypassPermissions",
			setup: func(h *harness, alternate string) {
				h.claudeGate(false)
				put(h.t, filepath.Join(os.Getenv("HOME"), ".claude", "settings.json"), `{}`)
				put(h.t, filepath.Join(alternate, "settings.json"), `{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"duolog gate --agent claude --event PreToolUse"}]}]}}`)
				h.t.Setenv("CLAUDE_CONFIG_DIR", alternate)
			}},
		// The hooks are approved in both homes, so only the folder trust the
		// worker's own home lacks is left to the preflight.
		{name: "codex folder trust only in the caller's CODEX_HOME", agent: "codex", model: "sol", entry: "codex: workspace-write, network: true", lead: "bypassPermissions",
			setup: func(h *harness, alternate string) {
				put(h.t, filepath.Join(alternate, "config.toml"), read(h.t, filepath.Join(os.Getenv("HOME"), ".codex", "config.toml")))
				h.codexHooksOnly()
				h.t.Setenv("CODEX_HOME", alternate)
			}},
		// Re-review, Important 1: duolog's HOME is not the account's home and
		// is not passed on, so the worker's agent falls back to a home the
		// environment does not name. Its settings cannot be read, whatever
		// duolog's own HOME holds.
		{name: "claude gate only under the caller's HOME, HOME not passed", agent: "claude", model: "sonnet", entry: "claude: bypassPermissions", lead: "bypassPermissions",
			passthrough: "[PATH, USER]",
			setup: func(h *harness, _ string) {
				h.claudeGate(false)
				put(h.t, filepath.Join(os.Getenv("HOME"), ".claude", "settings.json"), `{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"duolog gate --agent claude --event PreToolUse"}]}]}}`)
			}},
		{name: "codex trust only under the caller's HOME, HOME not passed", agent: "codex", model: "sol", entry: "codex: workspace-write, network: true", lead: "bypassPermissions",
			passthrough: "[PATH, USER]",
			setup:       func(h *harness, _ string) {}},
	} {
		h := newHarness(t)
		h.gateOnPath(true)
		tc.setup(h, t.TempDir())
		userDir, _ := os.UserConfigDir()
		passthrough := cmp.Or(tc.passthrough, "["+home+", USER]")
		put(t, filepath.Join(userDir, "duolog", "config.yaml"), "env_passthrough: "+passthrough+"\nworker_modes:\n  roles:\n    implement: {"+tc.entry+"}\n")
		var err error
		if h.cfg, err = configfile.Load(h.root); err != nil {
			t.Fatal(err)
		}
		h.leadNative("claude", mode.Full, tc.lead)
		out, err := h.runOn("implement", tc.agent, tc.model)
		if len(h.calls) != 0 || err == nil || !strings.Contains(out.Line, "gate preflight failed") {
			t.Errorf("%s: calls %d, %v %q", tc.name, len(h.calls), err, out.Line)
		}
	}
	// Passed on, the same directory is the worker's, and its gate counts.
	h := newHarness(t)
	h.gateOnPath(true)
	h.claudeGate(false)
	alternate := t.TempDir()
	put(t, filepath.Join(alternate, "settings.json"), `{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"duolog gate --agent claude --event PreToolUse"}]}]}}`)
	t.Setenv("CLAUDE_CONFIG_DIR", alternate)
	userDir, _ := os.UserConfigDir()
	put(t, filepath.Join(userDir, "duolog", "config.yaml"), "env_passthrough: ["+home+", USER, CLAUDE_CONFIG_DIR]\nworker_modes:\n  roles:\n    implement: {claude: bypassPermissions}\n")
	var err error
	if h.cfg, err = configfile.Load(h.root); err != nil {
		t.Fatal(err)
	}
	h.leadNative("claude", mode.Full, "bypassPermissions")
	if out, err := h.runOn("implement", "claude", "sonnet"); err != nil || len(h.calls) != 1 {
		t.Fatal(out.Line, err)
	}
	// A worker that does not rely on the gate still runs without a home
	// folder, and the lead learns that its settings could not be read.
	h = newHarness(t)
	h.claudeGate(true)
	userDir, _ = os.UserConfigDir()
	put(t, filepath.Join(userDir, "duolog", "config.yaml"), "env_passthrough: [PATH, USER]\n")
	if h.cfg, err = configfile.Load(h.root); err != nil {
		t.Fatal(err)
	}
	h.lead("claude", mode.Edits, "high")
	if out, err := h.runOn("implement", "claude", "sonnet"); err != nil || len(h.calls) != 1 || !strings.Contains(out.Line, "no home folder") {
		t.Fatal(out.Line, err)
	}
}

// Integration review, Important: the process the gate admits carries the
// whole launch, Codex's exec, its sandbox flag and both pins with the role's
// extras, and the runner launches exactly that, in the free and the paid
// variant; Claude's permission flag likewise.
func TestAdmittedArgvIsLaunchedArgv(t *testing.T) {
	for _, paid := range []bool{false, true} {
		h := newHarness(t)
		h.gateOnPath(true)
		safe := filepath.ToSlash(t.TempDir())
		h.workerModes("  roles:\n    implement: {codex: workspace-write, network: true, writable_roots: [\"" + safe + "\"]}\n")
		if paid {
			h.paidReads("codex", "claude")
			pa := h.cfg.PaidRuns.Agent("codex")
			pa.Allow, pa.DailyCapUSD = true, 1000
			h.cfg.PaidRuns.Set("codex", pa)
			h.gate.Config = h.cfg // workerModes loaded a new config
		}
		h.lead("codex", mode.Edits, "high")
		if out, err := h.run("implement"); err != nil {
			t.Fatal(paid, out.Line, err)
		}
		h.requireCalls(1)
		c := h.calls[0]
		if paid != (c.Process.Variant == corelaunch.Paid) {
			t.Fatalf("paid %t: variant %s", paid, c.Process.Variant)
		}
		// A TOML basic string escapes backslashes, as on Windows: the JSON
		// encoding PinCodexSandbox writes.
		roots, err := json.Marshal([]string{filepath.Clean(safe)})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"exec", "-s", "workspace-write", "-c", "sandbox_workspace_write.network_access=true",
			"-c", "sandbox_workspace_write.writable_roots=" + string(roots)}
		if !slices.Equal(c.Process.Launch, want) {
			t.Errorf("paid %t: admitted launch %v, want %v", paid, c.Process.Launch, want)
		}
		argv, admitted := argvOf(c), c.Process.LaunchArgv()
		if len(argv) < len(admitted) || !slices.Equal(argv[:len(admitted)], admitted) || slices.Index(argv[len(admitted):], "-s") >= 0 {
			t.Errorf("paid %t: launched %v, admitted %v", paid, argv, admitted)
		}
	}
	h := newHarness(t)
	h.claudeGate(true)
	h.gateOnPath(true)
	h.workerModes("  roles:\n    implement: {claude: bypassPermissions}\n")
	h.leadNative("claude", mode.Full, "bypassPermissions")
	if _, err := h.runOn("implement", "claude", "sonnet"); err != nil {
		t.Fatal(err)
	}
	c := h.calls[0]
	if want := []string{"-p", "--permission-mode", "bypassPermissions"}; !slices.Equal(c.Process.Launch, want) {
		t.Errorf("claude: admitted launch %v", c.Process.Launch)
	}
	if argv, admitted := argvOf(c), c.Process.LaunchArgv(); !slices.Equal(argv[:len(admitted)], admitted) || slices.Contains(argv[len(admitted):], "--permission-mode") {
		t.Errorf("claude: launched %v, admitted %v", argv, admitted)
	}
}

// symlinkTo makes a symlink to target in a new temporary folder.
func symlinkTo(t *testing.T, target string) string {
	t.Helper()
	link := filepath.Join(t.TempDir(), "worker-cache")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("no symlinks:", err)
	}
	return link
}

func TestLooserRunWithoutGateRowsWarns(t *testing.T) {
	h := newHarness(t)
	h.gitRepo() // changes() needs Git to see the edit
	h.claudeGate(true)
	h.gateOnPath(true)
	h.workerModes("  roles:\n    implement: {claude: bypassPermissions}\n")
	h.leadNative("claude", mode.Full, "bypassPermissions")
	h.change = "a.go" // the fake run edits a file and writes no gate row
	out, err := h.runOn("implement", "claude", "sonnet")
	if err != nil || !strings.Contains(out.Line, "gate saw no actions in native bypassPermissions") || !strings.Contains(out.Line, "native=bypassPermissions (role implement)") {
		t.Fatal(out.Line, err)
	}
	if !strings.Contains(read(t, filepath.Join(h.s.Dir, "usage.ndjson")), `"native":"bypassPermissions"`) {
		t.Fatal("usage record lacks native")
	}
}
