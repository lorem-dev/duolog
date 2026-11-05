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
	put(h.t, filepath.Join(h.root, ".codex", "hooks.json"), `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"duolog gate"}]}]}}`)
	s := fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"fixture-hash\"\n", filepath.Join(h.root, ".codex", "hooks.json")+":PreToolUse:0:0")
	put(h.t, filepath.Join(os.Getenv("HOME"), ".codex", "config.toml"), s)
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
	env := strings.Join(h.calls[0].Env, "\n")
	if !strings.Contains(env, "DUOLOG_MODE=edits") || !strings.Contains(env, "DUOLOG_NATIVE=") {
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
	if c := h.calls[0]; c.Native != "read-only" || c.Mode != mode.Plan || !strings.Contains(strings.Join(c.Env, "\n"), "DUOLOG_MODE=plan") {
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
