package delegate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/install"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/session"
)

// workerModes writes a user-level worker_modes section and reloads the config.
func (h *harness) workerModes(yaml string) {
	h.t.Helper()
	userDir, _ := os.UserConfigDir()
	put(h.t, filepath.Join(userDir, "duolog", "config.yaml"), "worker_modes:\n"+yaml)
	var err error
	if h.cfg, err = config.Load(h.root); err != nil {
		h.t.Fatal(err)
	}
}

// leadNative registers the lead and records its native mode name.
func (h *harness) leadNative(agent string, m mode.Mode, native string) {
	h.t.Helper()
	h.lead(agent, m, "high")
	if _, err := h.reg.ObserveLead(session.LeadObservation{ID: "lead-123456", Agent: agent, Mode: &m, Native: &native}, time.Now()); err != nil {
		h.t.Fatal(err)
	}
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

// gateOnPath replaces gateBinary for the test.
func (h *harness) gateOnPath(ok bool) {
	h.t.Helper()
	old := gateBinary
	h.t.Cleanup(func() { gateBinary = old })
	gateBinary = func(string) (string, error) {
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
// the approved gate is duolog's own command.
func codexHooks(t *testing.T) string {
	t.Helper()
	data, err := install.HooksJSON("codex", 30)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// runOn runs role on agent/model at effort medium, as --use does.
func (h *harness) runOn(role, agent, model string) (Outcome, error) {
	h.t.Helper()
	opts := Options{Entry: &config.Target{Agent: agent, Model: model, Effort: "medium"}, Person: true}
	return Run(WithOptions(context.Background(), opts), h.cfg, h.s, role, h.path, "topic")
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
		{"claude gate missing", "claude", "sonnet", func(h *harness) { h.claudeGate(false); h.gateOnPath(true) }, "claude: auto"},
		{"claude hooks off", "claude", "sonnet", func(h *harness) { h.claudeGate(true); h.hooksOff(); h.gateOnPath(true) }, "claude: auto"},
		{"binary missing", "claude", "sonnet", func(h *harness) { h.claudeGate(true); h.gateOnPath(false) }, "claude: auto"},
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
		}, "claude: auto"},
	} {
		h := newHarness(t)
		tc.setup(h)
		h.workerModes("  roles:\n    implement: {" + strings.ReplaceAll(tc.entry, "<state>", filepath.ToSlash(filepath.Join(h.root, ".duolog"))) + "}\n")
		h.leadNative("claude", mode.Edits, "auto")
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
	put(t, filepath.Join(bin, "claude"), "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(bin, "claude"), 0o755); err != nil {
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
		if h.cfg, err = config.Load(h.root); err != nil {
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
		if c := h.calls[0]; c.Native != "plan" {
			t.Errorf("%s: %s on %s, want plan (%s)", tc.name, c.Native, c.Route.Model, out.Line)
		}
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
	h.workerModes("  roles:\n    implement: {claude: auto}\n")
	h.leadNative("claude", mode.Edits, "auto")
	h.change = "a.go" // the fake run edits a file and writes no gate row
	out, err := h.runOn("implement", "claude", "sonnet")
	if err != nil || !strings.Contains(out.Line, "gate saw no actions in native auto") || !strings.Contains(out.Line, "native=auto (role implement)") {
		t.Fatal(out.Line, err)
	}
	if !strings.Contains(read(t, filepath.Join(h.s.Dir, "usage.ndjson")), `"native":"auto"`) {
		t.Fatal("usage record lacks native")
	}
}
