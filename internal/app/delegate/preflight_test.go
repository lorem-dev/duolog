package delegate

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/taskspec"
)

// checks sets the Check section of the harness task.
func (h *harness) checks(lines string) {
	h.t.Helper()
	put(h.t, h.path, "# Task: example\n\n## Goal\nDo work.\n\n## Read\n- a.go\n\n## Write\n- a.go\n\n## Limits\nStay focused.\n\n## Check\n"+lines+"\n")
}

func TestPreflightBlocksBeforeAnyWorkerStarts(t *testing.T) {
	h := newHarness(t)
	h.checks("`go test ./...`\n`go run ./cmd/x`\n`uv run git push`\n")
	out, err := h.run("implement")
	h.requireCalls(0)
	var pe *PreflightError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want a PreflightError", err)
	}
	var ne *NotStartedError
	if !errors.As(err, &ne) {
		t.Errorf("err = %v, want a NotStartedError", err)
	}
	if len(pe.Blocked) != 2 || pe.Blocked[0].Command != "go run ./cmd/x" || pe.Blocked[0].Decision != "ask" || pe.Blocked[1].Command != "uv run git push" || pe.Blocked[1].Decision != "deny" {
		t.Fatalf("blocked = %+v", pe.Blocked)
	}
	for _, want := range []string{"go run ./cmd/x: ask", "duolog remember 'go run ./cmd/x'", "uv run git push: deny", "--skip-preflight"} {
		if !strings.Contains(out.Line, want) {
			t.Errorf("line %q lacks %q", out.Line, want)
		}
	}
	if strings.Contains(out.Line, "remember 'uv run git push'") {
		t.Errorf("a deny must not offer remember: %q", out.Line)
	}
	if strings.Contains(out.Line, "go test") {
		t.Errorf("an allowed command is named: %q", out.Line)
	}
}

func TestPreflightPassesAllowedChecks(t *testing.T) {
	h := newHarness(t)
	h.checks("`go test ./...`\n`go vet ./...`\n")
	if _, err := h.run("implement"); err != nil {
		t.Fatal(err)
	}
	h.requireCalls(1)
}

func TestPreflightSkipOptionStartsTheWorker(t *testing.T) {
	h := newHarness(t)
	h.checks("`go run ./cmd/x`\n")
	if _, err := h.runWith("implement", Options{SkipPreflight: true}); err != nil {
		t.Fatal(err)
	}
	h.requireCalls(1)
}

func TestPreflightNamesUnreadableLinesButDoesNotBlock(t *testing.T) {
	h := newHarness(t)
	var stderr bytes.Buffer
	h.checks("Run the package tests only.\n`go test ./...`\n")
	if _, err := h.runWith("implement", Options{Stderr: &stderr}); err != nil {
		t.Fatal(err)
	}
	h.requireCalls(1)
	if !strings.Contains(stderr.String(), "Run the package tests only.") {
		t.Errorf("stderr %q does not name the skipped line", stderr.String())
	}
}

func TestPreflightSkippedLinesAreNamedWhenBlocked(t *testing.T) {
	h := newHarness(t)
	h.checks("see the Makefile\n`go run ./cmd/x`\n")
	out, err := h.run("implement")
	h.requireCalls(0)
	var pe *PreflightError
	if !errors.As(err, &pe) || len(pe.Skipped) != 1 || !strings.Contains(out.Line, "see the Makefile") {
		t.Fatalf("err %v, line %q", err, out.Line)
	}
}

// fallbackRoutes makes the first route a Claude haiku worker held at level plan,
// where no Check command runs, and the fallback a Codex worker.
func (h *harness) fallbackRoutes() {
	h.t.Helper()
	h.claudeGate(true)
	h.gateOnPath(true)
	userDir, _ := os.UserConfigDir()
	put(h.t, filepath.Join(userDir, "duolog", "config.yaml"), "roles:\n  implement:\n    use: {agent: claude, model: haiku, effort: medium}\n    fallback: {agent: codex, model: sol, effort: medium}\nworker_modes:\n  families:\n    haiku: {claude: plan}\n")
	var err error
	if h.cfg, err = configfile.Load(h.root); err != nil {
		h.t.Fatal(err)
	}
	h.cfg.Routing.SmallTaskFiles = 0
	h.leadNative("claude", mode.Edits, "acceptEdits")
}

// The Check commands are judged for the route that starts: a first route held at
// plan does not refuse a task its fallback would run.
func TestPreflightJudgesTheRouteThatStarts(t *testing.T) {
	h := newHarness(t)
	h.fallbackRoutes()
	h.checks("`go test ./...`\n")
	if _, err := h.run("implement"); err != nil {
		t.Fatal(err)
	}
	h.requireCalls(1)
	if got := h.calls[0].Route.Agent; got != "codex" {
		t.Errorf("worker agent = %s, want the fallback codex", got)
	}
}

// When the fallback is blocked too, the task is refused and no worker starts.
func TestPreflightRefusesWhenTheFallbackIsBlockedToo(t *testing.T) {
	h := newHarness(t)
	h.fallbackRoutes()
	h.checks("`go run ./cmd/x`\n")
	_, err := h.run("implement")
	var pe *PreflightError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want a PreflightError", err)
	}
	h.requireCalls(0)
}

// A route the gate stops is not judged as allowed: with only the held route,
// the task is refused.
func TestPreflightRefusesTheOnlyRouteHeldAtPlan(t *testing.T) {
	h := newHarness(t)
	h.fallbackRoutes()
	h.unavailable("codex")
	h.checks("`go test ./...`\n")
	_, err := h.run("implement")
	var pe *PreflightError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want a PreflightError", err)
	}
	h.requireCalls(0)
}

// A blocked route whose fallback is the lead's own agent keeps the refusal: the
// task is not placed in the lead with a bare "run here".
func TestPreflightRefusalSurvivesAFallbackToTheLead(t *testing.T) {
	h := newHarness(t)
	h.claudeGate(true)
	h.gateOnPath(true)
	userDir, _ := os.UserConfigDir()
	put(t, filepath.Join(userDir, "duolog", "config.yaml"), "roles:\n  implement:\n    use: {agent: codex, model: sol, effort: medium}\n    fallback: {agent: claude, model: haiku, effort: medium}\n")
	var err error
	if h.cfg, err = configfile.Load(h.root); err != nil {
		t.Fatal(err)
	}
	h.cfg.Routing.SmallTaskFiles = 0
	h.leadNative("claude", mode.Edits, "acceptEdits")
	h.checks("`go run ./cmd/x`\n")
	out, err := h.run("implement")
	var pe *PreflightError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, out = %+v, want a PreflightError", err, out)
	}
	if out.RunHere != nil {
		t.Errorf("task placed in the lead: %+v", out.RunHere)
	}
	h.requireCalls(0)
}

// A pinned target whose Check is blocked ends with the preflight error, not
// with "requested target is unavailable".
func TestPreflightRefusesAPinnedTarget(t *testing.T) {
	h := newHarness(t)
	h.checks("`go run ./cmd/x`\n")
	_, err := h.runWith("implement", Options{Entry: &config.Target{Agent: "codex", Model: "sol", Effort: "medium"}, Person: true})
	var pe *PreflightError
	if !errors.As(err, &pe) || len(pe.Blocked) != 1 {
		t.Fatalf("err = %v, want a PreflightError with one blocked command", err)
	}
	h.requireCalls(0)
}

// A replay and a judge run are not the lead's hand-over: no preflight.
func TestPreflightSkipsReplayAndJudgeRuns(t *testing.T) {
	for name, opts := range map[string]Options{"replay": {ReplayOf: "task-1"}, "judge": {Judge: true}} {
		h := newHarness(t)
		h.checks("`go run ./cmd/x`\n")
		if _, err := h.runWith("implement", opts); err != nil {
			var pe *PreflightError
			if errors.As(err, &pe) {
				t.Errorf("%s: preflight ran: %v", name, err)
			}
		}
	}
}

// The Check lines that were not read as a command are in the outcome of every
// run, not only a refusal.
func TestPreflightOutcomeCarriesSkippedLines(t *testing.T) {
	h := newHarness(t)
	h.checks("Run the package tests only.\n`go test ./...`\n")
	out, err := h.run("implement")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.SkippedChecks) != 1 || out.SkippedChecks[0] != "Run the package tests only." {
		t.Errorf("SkippedChecks = %q", out.SkippedChecks)
	}
	r := RecordFor(Record{}, out, nil, time.Now())
	if len(r.Skipped) != 1 {
		t.Errorf("record skipped = %q", r.Skipped)
	}
}

// A task rendered from the MCP fields is preflighted: its Check items are
// commands.
func TestPreflightJudgesRenderedCheckItems(t *testing.T) {
	h := newHarness(t)
	data, err := taskspec.Render(taskspec.Spec{Goal: "Do work.", Read: []string{"a.go"}, Write: []string{"a.go"}, Limits: []string{"Stay focused."}, Check: []string{"go run ./cmd/x"}})
	if err != nil {
		t.Fatal(err)
	}
	put(t, h.path, string(data))
	_, err = h.run("implement")
	var pe *PreflightError
	if !errors.As(err, &pe) || len(pe.Blocked) != 1 || pe.Blocked[0].Command != "go run ./cmd/x" {
		t.Fatalf("err = %v, want go run blocked", err)
	}
	h.requireCalls(0)
}

func TestPreflightLeavesReadOnlyRolesAlone(t *testing.T) {
	h := newHarness(t)
	h.checks("`go run ./cmd/x`\n")
	if _, err := h.run("review_intermediate"); err != nil {
		t.Fatal(err)
	}
	h.requireCalls(1)
}

func TestPreflightQuotesTheRememberCommand(t *testing.T) {
	if got := RememberHint("make it's"); got != `duolog remember 'make it'\''s'` {
		t.Errorf("hint = %s", got)
	}
}

func TestPreflightRunNamesBothSectionsBeforeWorkerStarts(t *testing.T) {
	// Kills ignoring Run, stopping after Check, or losing section provenance.
	h := newHarness(t)
	h.checks("`go run ./cmd/check`\n")
	data, err := os.ReadFile(h.path)
	if err != nil {
		t.Fatal(err)
	}
	put(t, h.path, strings.Replace(string(data), "## Check", "## Run\n`go run ./cmd/generate`\n## Check", 1))
	out, err := h.run("implement")
	h.requireCalls(0)
	var pe *PreflightError
	if !errors.As(err, &pe) || len(pe.Blocked) != 2 {
		t.Fatalf("err=%v", err)
	}
	for _, want := range []string{"Run: go run ./cmd/generate: ask", "Check: go run ./cmd/check: ask"} {
		if !strings.Contains(out.Line, want) {
			t.Errorf("%q lacks %q", out.Line, want)
		}
	}
}

func TestPreflightRunUsesRouteWorkerMode(t *testing.T) {
	// Kills judging Run in the lead mode or ignoring it on fallback routes.
	h := newHarness(t)
	h.fallbackRoutes()
	h.checks("- none\n")
	data, err := os.ReadFile(h.path)
	if err != nil {
		t.Fatal(err)
	}
	put(t, h.path, strings.Replace(string(data), "## Check", "## Run\n`go test ./pkg`\n## Check", 1))
	if _, err := h.run("implement"); err != nil {
		t.Fatal(err)
	}
	h.requireCalls(1)
	if h.calls[0].Route.Agent != "codex" {
		t.Fatalf("started %s", h.calls[0].Route.Agent)
	}
}

// Kills dropping the Run lines that were not read as commands from the
// outcome of a run that starts.
func TestPreflightOutcomeCarriesSkippedRunLines(t *testing.T) {
	h := newHarness(t)
	h.checks("`go test ./...`\n")
	data, err := os.ReadFile(h.path)
	if err != nil {
		t.Fatal(err)
	}
	put(t, h.path, strings.Replace(string(data), "## Check", "## Run\nRegenerate the schema.\n## Check", 1))
	out, err := h.run("implement")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.SkippedChecks) != 1 || out.SkippedChecks[0] != "Run: Regenerate the schema." {
		t.Errorf("SkippedChecks = %q", out.SkippedChecks)
	}
}
