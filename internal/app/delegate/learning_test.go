package delegate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/auth"
	"github.com/lorem-dev/duolog/internal/capability"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/usage"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/runner"
	"github.com/lorem-dev/duolog/internal/session"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main", "-c", "core.autocrlf=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// enableLearning turns learning on in the project config and reloads it.
func (h *harness) enableLearning() {
	h.t.Helper()
	put(h.t, filepath.Join(h.s.Dir, "config.yaml"), "learning:\n  enabled: true\n")
	var err error
	if h.cfg, err = configfile.Load(h.root); err != nil {
		h.t.Fatal(err)
	}
}

// learning enables learning, makes the project a Git repository with one
// commit and, with startTask, opens a task for the harness lead.
func (h *harness) learning(startTask bool) {
	h.t.Helper()
	h.enableLearning()
	put(h.t, filepath.Join(h.root, ".gitignore"), ".duolog/\n")
	gitIn(h.t, h.root, "init", "-q")
	gitIn(h.t, h.root, "add", "-A")
	gitIn(h.t, h.root, "commit", "-q", "-m", "start")
	if startTask {
		if _, err := h.journal().StartTask("lead-123456", "example", h.head(), time.Now()); err != nil {
			h.t.Fatal(err)
		}
	}
}
func (h *harness) head() string { return gitIn(h.t, h.root, "rev-parse", "HEAD") }
func (h *harness) journal() *journal.Journal {
	h.t.Helper()
	j, err := journal.Open(h.s.Dir)
	if err != nil {
		h.t.Fatal(err)
	}
	return j
}
func (h *harness) records() []journal.Delegation {
	h.t.Helper()
	recs, err := journal.ReadAll[journal.Delegation](h.journal(), "delegations.ndjson")
	if err != nil {
		h.t.Fatal(err)
	}
	return recs
}
func (h *harness) record() journal.Delegation {
	h.t.Helper()
	recs := h.records()
	if len(recs) != 1 {
		h.t.Fatalf("records=%d want 1: %+v", len(recs), recs)
	}
	return recs[0]
}
func (h *harness) runWith(role string, opts Options) (Outcome, error) {
	h.t.Helper()
	return Run(WithOptions(context.Background(), opts), h.cfg, h.s, role, h.path, "topic")
}
func diagnosis() map[string]string { return map[string]string{"output": "diagnosis"} }
func noJournal(t *testing.T, h *harness) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(h.s.Dir, "journal")); !os.IsNotExist(err) {
		t.Fatalf("journal written: %v", err)
	}
}
func target(key string) *config.Target {
	e, err := config.ParseEntryKey(key)
	if err != nil {
		panic(err)
	}
	t := e.Target()
	return &t
}

func TestLearningOffWritesNoJournal(t *testing.T) {
	h := newHarness(t)
	if _, err := h.run("implement"); err != nil {
		t.Fatal(err)
	}
	noJournal(t, h)
}

// With learning off, flags for learning change nothing: the line is the one
// delegate printed before learning mode existed, and no file is added.
func TestLearningOffOutputUnchanged(t *testing.T) {
	h := newHarness(t)
	put(t, filepath.Join(h.root, ".gitignore"), ".duolog/\n")
	gitIn(t, h.root, "init", "-q")
	out, err := h.runWith("implement", Options{Features: diagnosis(), Class: "no_such_class"})
	if err != nil {
		t.Fatal(err)
	}
	h.requireCalls(1)
	want := "done=true summary=Completed. files=a.go"
	if out.Line != want {
		t.Fatalf("line %q, want %q", out.Line, want)
	}
	if h.calls[0].Route.Model == "" || route.Normalize("codex", h.calls[0].Route.Model) != "sol" {
		t.Fatal(h.calls[0].Route)
	}
	noJournal(t, h)
	var names []string
	_ = filepath.Walk(h.s.Dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			rel, _ := filepath.Rel(h.s.Dir, path)
			names = append(names, filepath.ToSlash(rel))
		}
		return nil
	})
	for _, name := range names {
		if strings.HasPrefix(name, "journal") || strings.HasSuffix(name, ".patch") {
			t.Fatal(names)
		}
	}
}

func TestLearningOpensImplicitTask(t *testing.T) {
	h := newHarness(t)
	h.learning(false)
	out, err := h.run("implement")
	if err != nil || !h.record().Done {
		t.Fatal(out, err)
	}
	h.requireCalls(1)
	task, ok, err := h.journal().CurrentTask("lead-123456")
	if err != nil || !ok || task.Base != h.head() || task.Title != "(implicit)" {
		t.Fatal(task, ok, err)
	}
	if h.record().Task != task.ID || !strings.Contains(out.Line, "duolog task finish") {
		t.Fatal(h.record(), out.Line)
	}
}

func TestLearningUnclassifiedKeepsRoleTarget(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	out, err := h.run("review_final")
	if err != nil {
		t.Fatal(out, err)
	}
	h.requireCalls(1)
	if r := h.calls[0].Route; r.Agent != "claude" || route.Normalize("claude", r.Model) != "opus" {
		t.Fatal(r)
	}
	rec := h.record()
	if rec.Class != "unclassified" || rec.RuleClass != "unclassified" || rec.Why != "role" || rec.Entry != "" {
		t.Fatalf("%+v", rec)
	}
	if !strings.Contains(out.Line, "--features") {
		t.Fatal(out.Line)
	}
}

func TestLearningRoutesByClass(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	out, err := h.runWith("debug_hard", Options{Features: diagnosis()})
	if err != nil {
		t.Fatal(out, err)
	}
	h.requireCalls(1)
	if r := h.calls[0].Route; r.Agent != "codex" || route.Normalize("codex", r.Model) != "terra" || r.Model == "terra" || r.Effort != "medium" {
		t.Fatal(r)
	}
	rec := h.record()
	if rec.Class != "debug" || rec.RuleClass != "debug" || rec.Why != "prior" || rec.Entry != "codex/terra/medium" || rec.Ladder != "debug" || rec.Effort != "medium" || rec.Features["output"] != "diagnosis" {
		t.Fatalf("%+v", rec)
	}
}

func TestLearningStatedClassRecordsBoth(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	if out, err := h.runWith("debug_hard", Options{Class: "debug_multi", Features: diagnosis()}); err != nil {
		t.Fatal(out, err)
	}
	if r := h.calls[0].Route; r.Agent != "claude" || route.Normalize("claude", r.Model) != "opus" {
		t.Fatal(r)
	}
	rec := h.record()
	if rec.Class != "debug_multi" || rec.StatedClass != "debug_multi" || rec.RuleClass != "debug" || rec.Entry != "claude/opus/high" {
		t.Fatalf("%+v", rec)
	}
}

func TestLearningRefusesUnknownClass(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	out, err := h.runWith("debug_hard", Options{Class: "nope"})
	if err == nil || !strings.Contains(out.Line, `unknown class "nope"`) {
		t.Fatal(out, err)
	}
	h.requireCalls(0)
}

func TestLearningEntryEqualToLeadRunsWorker(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Edits, "high")
	h.learning(true)
	out, err := h.runWith("plan", Options{Features: map[string]string{"output": "plan"}})
	if err != nil || out.RunHere != nil {
		t.Fatal(out, err)
	}
	h.requireCalls(1)
	if rec := h.record(); rec.Class != "skeleton" || rec.Ladder != "skeleton.plan" || rec.Entry != "claude/opus/high" {
		t.Fatalf("%+v", rec)
	}
}

func TestLearningCheaperLeadStillRunsHere(t *testing.T) {
	h := newHarness(t)
	h.register(session.Lead{ID: "lead-123456", Target: config.Target{Agent: "claude", Model: "haiku", Effort: "high"}, Mode: mode.Edits})
	h.learning(true)
	out, err := h.runWith("docs", Options{Features: map[string]string{"output": "docs"}})
	if err != nil || out.RunHere == nil {
		t.Fatal(out, err)
	}
	h.requireCalls(0)
	if rec := h.record(); !rec.RunHere || rec.Done || rec.Entry != "claude/sonnet/medium" {
		t.Fatalf("%+v", rec)
	}
}

func TestLearningRecordsReplayFiles(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	h.change = "b.go"
	inner := runWorker
	runWorker = func(ctx context.Context, s runner.Spec) (runner.Result, error) {
		put(t, filepath.Join(s.TaskDir, "answer.json"), `{"done":true}`)
		return inner(ctx, s)
	}
	if out, err := h.run("implement"); err != nil {
		t.Fatal(out, err)
	}
	rec := h.record()
	dir := h.journal().TaskDir(rec.Task, rec.ID)
	for _, name := range []string{"task.md", "answer.json", "before.patch", "result.patch"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		// Windows keeps only a read-only bit, so there the file need only exist.
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
			t.Fatalf("%s mode %v", name, info.Mode())
		}
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() || runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
		t.Fatal(info, err)
	}
	if read(t, filepath.Join(dir, "task.md")) != read(t, filepath.Join(h.calls[0].TaskDir, "task.md")) {
		t.Fatal("task copy differs")
	}
	if !strings.Contains(read(t, filepath.Join(dir, "result.patch")), "b.go") {
		t.Fatal(read(t, filepath.Join(dir, "result.patch")))
	}
	if len(rec.Files) != 1 || rec.Files[0].Path != "b.go" || rec.Files[0].Added != 1 || rec.Base != h.head() || rec.BeforeTree == "" || rec.AfterTree == "" {
		t.Fatalf("%+v", rec)
	}
	if rec.ID != filepath.Base(h.calls[0].TaskDir) {
		t.Fatal(rec.ID)
	}
}

func TestLearningRecordsCost(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	for i := 0; i < 2; i++ {
		if out, err := h.run("implement"); err != nil {
			t.Fatal(out, err)
		}
	}
	recs := h.records()
	if len(recs) != 2 {
		t.Fatal(recs)
	}
	for i, rec := range recs {
		if rec.USD <= 0 || !rec.Priced || rec.Seconds < 0 || !rec.Done || rec.Step != i+1 || rec.Input != 12 || rec.Output != 3 || rec.Lead != "lead-123456" || rec.Role != "implement" || rec.Agent != "codex" {
			t.Fatalf("%d: %+v", i, rec)
		}
	}
	if recs[0].Task != recs[1].Task || recs[0].ID == recs[1].ID {
		t.Fatal(recs)
	}
}

func TestLearningRoleMismatchWarns(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	out, err := h.runWith("review_final", Options{Class: "tests"})
	if err != nil {
		t.Fatal(out, err)
	}
	if rec := h.record(); !rec.RoleMismatch || rec.Class != "tests" {
		t.Fatalf("%+v", rec)
	}
	if !strings.Contains(out.Line, "class tests does not list role review_final") {
		t.Fatal(out.Line)
	}
	// Access stays the role's.
	if h.calls[0].Route.Sandbox != "read-only" {
		t.Fatal(h.calls[0].Route)
	}
}

func TestLearningLineNamesDelegation(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	out, err := h.runWith("debug_hard", Options{Features: diagnosis()})
	if err != nil {
		t.Fatal(out, err)
	}
	if !strings.Contains(out.Line, "; id="+filepath.Base(h.calls[0].TaskDir)) || !strings.Contains(out.Line, "id=task-") || !strings.Contains(out.Line, "class=debug") || !strings.Contains(out.Line, "entry=codex/terra/medium") {
		t.Fatal(out.Line)
	}
	if strings.Contains(out.Line, "duolog task start") || strings.Contains(out.Line, "unclassified") {
		t.Fatal(out.Line)
	}
}

func TestLearningCountsGateDenials(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	// A decision from before the run does not count.
	if err := gate.Log(h.s, gate.Action{Kind: "exec", Target: "old", Role: "implement"}, gate.Decision{Kind: gate.Deny}); err != nil {
		t.Fatal(err)
	}
	inner := runWorker
	runWorker = func(ctx context.Context, s runner.Spec) (runner.Result, error) {
		for _, d := range []struct{ role, kind string }{{"implement", gate.Deny}, {"tests", gate.Deny}, {"implement", gate.Allow}, {"implement", gate.Ask}} {
			if err := gate.Log(h.s, gate.Action{Kind: "exec", Target: "rm -rf x", Role: d.role}, gate.Decision{Kind: d.kind}); err != nil {
				t.Fatal(err)
			}
		}
		return inner(ctx, s)
	}
	if out, err := h.run("implement"); err != nil {
		t.Fatal(out, err)
	}
	if rec := h.record(); rec.GateDenials != 1 || rec.GateAsks != 1 {
		t.Fatalf("%+v", rec)
	}
}

func TestLearningPatchesStayRelativeToTaskBase(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	base := h.head()
	put(t, filepath.Join(h.root, "c.go"), "package c\n")
	gitIn(t, h.root, "add", "c.go")
	gitIn(t, h.root, "commit", "-q", "-m", "c")
	if out, err := h.run("implement"); err != nil {
		t.Fatal(out, err)
	}
	rec := h.record()
	if rec.Base != base {
		t.Fatal(rec.Base, base)
	}
	other := filepath.Join(t.TempDir(), "replay")
	gitIn(t, h.root, "worktree", "add", "-q", "--detach", other, base)
	if _, err := os.Stat(filepath.Join(other, "c.go")); !os.IsNotExist(err) {
		t.Fatal("c.go already at base")
	}
	patch, err := os.ReadFile(filepath.Join(h.journal().TaskDir(rec.Task, rec.ID), "before.patch"))
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Apply(other, patch); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(other, "c.go")) != "package c\n" {
		t.Fatal("before.patch does not reproduce the commit")
	}
}

func TestLearningOutsideGitStillDelegates(t *testing.T) {
	h := newHarness(t)
	h.enableLearning()
	out, err := h.run("implement")
	if err != nil {
		t.Fatal(out, err)
	}
	rec := h.record()
	if !rec.Done || rec.Base != "" || rec.BeforeTree != "" {
		t.Fatalf("%+v", rec)
	}
	dir := h.journal().TaskDir(rec.Task, rec.ID)
	if _, err := os.Stat(filepath.Join(dir, "before.patch")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "task.md")); err != nil {
		t.Fatal(err)
	}
}

func TestJudgeRunIsNotJournalled(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	if out, err := h.runWith("review_intermediate", Options{Judge: true, Entry: target("claude/opus/high")}); err != nil {
		t.Fatal(out, err)
	}
	h.requireCalls(1)
	if recs := h.records(); len(recs) != 0 {
		t.Fatal(recs)
	}
	if _, err := os.Stat(filepath.Join(h.s.Dir, "journal", "delegations.ndjson")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestJudgeRunRoutesToEntry(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	out, err := h.runWith("review_intermediate", Options{Judge: true, Entry: target("claude/opus/high")})
	if err != nil {
		t.Fatal(out, err)
	}
	if r := h.calls[0].Route; r.Agent != "claude" || route.Normalize("claude", r.Model) != "opus" || r.Effort != "high" {
		t.Fatal(r)
	}
	if strings.Contains(out.Line, "id=") {
		t.Fatal(out.Line)
	}
}

func TestLearningUseRecordsPerson(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	out, err := h.runWith("debug_hard", Options{Entry: target("claude/opus/high"), Person: true, Features: diagnosis()})
	if err != nil {
		t.Fatal(out, err)
	}
	if r := h.calls[0].Route; r.Agent != "claude" || route.Normalize("claude", r.Model) != "opus" {
		t.Fatal(r)
	}
	rec := h.record()
	if rec.Why != "person" || rec.Class != "debug" || !rec.Done || rec.Entry != "claude/opus/high" || rec.Ladder != "debug" {
		t.Fatalf("%+v", rec)
	}
}

func TestUseWorksWithLearningOff(t *testing.T) {
	h := newHarness(t)
	if out, err := h.runWith("review_intermediate", Options{Entry: target("codex/sol/high"), Person: true}); err != nil {
		t.Fatal(out, err)
	}
	if r := h.calls[0].Route; r.Agent != "codex" || route.Normalize("codex", r.Model) != "sol" || r.Effort != "high" {
		t.Fatal(r)
	}
	noJournal(t, h)
}

func TestJudgeRoutesToEntryWithoutJournalling(t *testing.T) {
	h := newHarness(t)
	h.learning(false)
	h.register(session.Lead{ID: "lead-123456", Target: config.Target{Agent: "claude", Model: "haiku", Effort: "high"}, Mode: mode.Full})
	spec := h.cfg.Roles["review_final"]
	spec.Context = "minimal"
	h.cfg.Roles["review_final"] = spec
	out, err := h.runWith("review_final", Options{Judge: true, Entry: target("codex/sol/high")})
	if err != nil || out.RunHere != nil {
		t.Fatal(out, err)
	}
	h.requireCalls(1)
	if r := h.calls[0].Route; r.Agent != "codex" || route.Normalize(r.Agent, r.Model) != "sol" || r.Effort != "high" {
		t.Fatal(r)
	}
	if recs := h.records(); len(recs) != 0 {
		t.Fatal(recs)
	}
	if _, ok, err := h.journal().CurrentTask("lead-123456"); err != nil || ok {
		t.Fatal("judge opened a task", err)
	}
}

func TestReplayRecordCarriesOriginalTask(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	if out, err := h.runWith("debug_hard", Options{Features: diagnosis()}); err != nil {
		t.Fatal(out, err)
	}
	original := h.record()
	// The replay needs no open task.
	if _, err := h.journal().CloseTask("lead-123456"); err != nil {
		t.Fatal(err)
	}
	h.change = "b.go"
	if out, err := h.runWith("debug_hard", Options{Entry: target("codex/sol/medium"), ReplayOf: original.ID}); err != nil {
		t.Fatal(out, err)
	}
	recs := h.records()
	if len(recs) != 2 {
		t.Fatal(recs)
	}
	replay := recs[1]
	if replay.ProfileOf != original.ID || replay.Task != original.Task || replay.Step != 0 || replay.Why != "forced" || replay.Class != "debug" || replay.Ladder != "debug" || replay.Entry != "codex/sol/medium" {
		t.Fatalf("%+v", replay)
	}
	if _, err := os.Stat(h.journal().TaskDir(original.Task, replay.ID)); !os.IsNotExist(err) {
		t.Fatal("replay wrote files", err)
	}
	if _, ok, _ := h.journal().CurrentTask("lead-123456"); ok {
		t.Fatal("replay opened a task")
	}
}

func TestWorkRootOptionIsUsed(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	other := filepath.Join(t.TempDir(), "work")
	gitIn(t, h.root, "worktree", "add", "-q", "--detach", other, "HEAD")
	put(t, filepath.Join(other, "before.go"), "before\n")
	// Codex trusts hooks per folder: the work root needs its own entry.
	trusted := ""
	for _, dir := range []string{h.root, other} {
		trusted += fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n[hooks.state.%q]\ntrusted_hash = \"fixture-hash\"\n", dir, filepath.Join(dir, ".codex", "hooks.json")+":PreToolUse:0:0")
	}
	put(t, filepath.Join(os.Getenv("HOME"), ".codex", "config.toml"), trusted)
	runWorker = func(_ context.Context, s runner.Spec) (runner.Result, error) {
		h.calls = append(h.calls, s)
		put(t, filepath.Join(s.Cwd, "made.go"), "made\n")
		return runner.Result{Done: true, Summary: "ok", Files: []string{"made.go"}, Needs: []string{}}, nil
	}
	if out, err := h.runWith("implement", Options{WorkRoot: other}); err != nil {
		t.Fatal(out, err)
	}
	if h.calls[0].Cwd != other {
		t.Fatal(h.calls[0].Cwd)
	}
	if _, err := os.Stat(filepath.Join(h.root, "made.go")); !os.IsNotExist(err) {
		t.Fatal("worker ran in the current directory")
	}
	rec := h.record()
	dir := h.journal().TaskDir(rec.Task, rec.ID)
	if !strings.Contains(read(t, filepath.Join(dir, "before.patch")), "before.go") || !strings.Contains(read(t, filepath.Join(dir, "result.patch")), "made.go") {
		t.Fatal(read(t, filepath.Join(dir, "before.patch")), read(t, filepath.Join(dir, "result.patch")))
	}
	if len(rec.Files) != 1 || rec.Files[0].Path != "made.go" {
		t.Fatalf("%+v", rec)
	}
}

func TestCalibrationRunWritesNoJournal(t *testing.T) {
	h := newHarness(t)
	h.learning(false)
	other := filepath.Join(t.TempDir(), "work")
	gitIn(t, h.root, "worktree", "add", "-q", "--detach", other, "HEAD")
	trusted := ""
	for _, dir := range []string{h.root, other} {
		trusted += fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n[hooks.state.%q]\ntrusted_hash = \"fixture-hash\"\n", dir, filepath.Join(dir, ".codex", "hooks.json")+":PreToolUse:0:0")
	}
	put(t, filepath.Join(os.Getenv("HOME"), ".codex", "config.toml"), trusted)
	e := *target("codex/sol/high")
	out, err := h.runWith("implement", Options{Calibration: true, Entry: &e, WorkRoot: other})
	if err != nil {
		t.Fatal(out, err)
	}
	h.requireCalls(1)
	if h.calls[0].Cwd != other {
		t.Fatal(h.calls[0].Cwd)
	}
	if _, err := os.Stat(filepath.Join(h.s.Dir, "journal", "delegations.ndjson")); !os.IsNotExist(err) {
		t.Fatal("delegations.ndjson written", err)
	}
	if _, ok, err := h.journal().CurrentTask("lead-123456"); err != nil || ok {
		t.Fatal("calibration run opened a task", err)
	}
	if out.Result.Usage.Input != 12 || out.Result.Usage.Output != 3 {
		t.Fatalf("%+v", out.Result.Usage)
	}
	if strings.Contains(out.Line, "id=") {
		t.Fatal(out.Line)
	}
}

// A calibration cell runs in an exported tree: its own repository outside
// the project, not a linked worktree, so the project's .duolog/tasks is
// outside both the work root and its state root.
func TestCalibrationTaskCopyInExportedTree(t *testing.T) {
	h := newHarness(t)
	tree := filepath.Join(t.TempDir(), "tree-0")
	put(t, filepath.Join(tree, "a.go"), "package a\n")
	// duolog's own gate command: a calibration cell runs only when the gate
	// is shown to run, and Codex skips a hook that is not the approved one.
	put(t, filepath.Join(tree, ".codex", "hooks.json"), codexHooks(t))
	gitIn(t, tree, "init", "-q")
	gitIn(t, tree, "add", "-A")
	gitIn(t, tree, "commit", "-q", "-m", "calibration base")
	put(t, filepath.Join(os.Getenv("HOME"), ".codex", "config.toml"), fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n[hooks.state.%q]\ntrusted_hash = \"fixture-hash\"\n", tree, filepath.Join(tree, ".codex", "hooks.json")+":PreToolUse:0:0"))
	e := *target("codex/sol/high")
	out, err := h.runWith("implement", Options{Calibration: true, Entry: &e, WorkRoot: tree})
	if err != nil {
		t.Fatal(out, err)
	}
	h.requireCalls(1)
	if h.calls[0].Cwd != tree {
		t.Fatalf("worker ran in %s, want %s", h.calls[0].Cwd, tree)
	}
	if rel, err := filepath.Rel(filepath.Join(tree, ".duolog", "tasks"), h.calls[0].TaskDir); err != nil || !filepath.IsLocal(rel) {
		t.Fatalf("task copy in %s, want it under the tree's .duolog/tasks", h.calls[0].TaskDir)
	}
}

// Re-review, Important 2: a calibration cell runs only when the gate shows
// in the worker's own environment. The review's inputs: the gate, or the
// Codex folder trust, only in the caller's CLAUDE_CONFIG_DIR or CODEX_HOME,
// env_passthrough without them, and a worker mode that is not looser.
func TestCalibrationNeedsTheGateInTheWorkersEnvironment(t *testing.T) {
	gate := `{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"duolog gate --agent claude --event PreToolUse"}]}]}}`
	for _, tc := range []struct {
		name, entry string
		setup       func(h *harness, alternate string)
	}{
		{"claude gate only in the caller's CLAUDE_CONFIG_DIR", "claude/sonnet/high", func(h *harness, alternate string) {
			h.claudeGate(false) // the project's own, which newHarness installs
			put(h.t, filepath.Join(alternate, "settings.json"), gate)
			h.t.Setenv("CLAUDE_CONFIG_DIR", alternate)
		}},
		{"codex folder trust only in the caller's CODEX_HOME", "codex/sol/high", func(h *harness, alternate string) {
			put(h.t, filepath.Join(alternate, "config.toml"), read(h.t, filepath.Join(os.Getenv("HOME"), ".codex", "config.toml")))
			h.codexHooksOnly()
			h.t.Setenv("CODEX_HOME", alternate)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			userDir, _ := os.UserConfigDir()
			put(t, filepath.Join(userDir, "duolog", "config.yaml"), "env_passthrough: [PATH, HOME, USER]\n")
			var err error
			if h.cfg, err = configfile.Load(h.root); err != nil {
				t.Fatal(err)
			}
			tc.setup(h, t.TempDir())
			h.lead("claude", mode.Edits, "high")
			e := *target(tc.entry)
			out, err := h.runWith("implement", Options{Calibration: true, Entry: &e})
			if err == nil || !strings.Contains(err.Error(), "gate") {
				t.Fatalf("err %v, line %q; want a refusal that names the gate", err, out.Line)
			}
			h.requireCalls(0)
			var notStarted *NotStartedError
			if !errors.As(err, &notStarted) {
				t.Fatalf("refusal %v is not a NotStartedError", err)
			}
		})
	}
}

// A calibration delegation refused before its worker starts, by the task,
// the lead or the preflight, returns a NotStartedError, so the run releases
// the step's charge; a worker that started and failed does not.
func TestCalibrationRefusalsBeforeWorkerAreNotStarted(t *testing.T) {
	for _, tc := range []struct {
		name    string
		setup   func(h *harness, opts *Options)
		started bool
	}{
		{"task", func(h *harness, _ *Options) { put(h.t, h.path, "# Task: example\n\nno sections\n") }, false},
		{"lead", func(_ *harness, opts *Options) { opts.Lead = "lead-gone" }, false},
		{"preflight", func(h *harness, _ *Options) {
			h.auth.agent = map[string]*auth.Problem{"codex": {Agent: "codex", Reason: "not logged in"}}
			h.auth.login = auth.NeedsTerminal
		}, false},
		{"worker", func(h *harness, _ *Options) { h.errs = []error{errors.New("worker crashed")} }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := authHarness(t)
			e := *target("codex/sol/high")
			opts := Options{Calibration: true, Entry: &e}
			tc.setup(h, &opts)
			_, err := h.runWith("implement", opts)
			var notStarted *NotStartedError
			if err == nil || errors.As(err, &notStarted) == tc.started {
				t.Fatalf("err = %v, want a refusal with not started %v", err, !tc.started)
			}
			if tc.started {
				h.requireCalls(1)
			} else {
				h.requireCalls(0)
			}
		})
	}
}

func TestOptionsFromReadsContext(t *testing.T) {
	entry := target("codex/sol/high")
	got := OptionsFrom(WithOptions(context.Background(), Options{Entry: entry, ReplayOf: "task-1", WorkRoot: "/w"}))
	if got.Entry != entry || got.ReplayOf != "task-1" || got.WorkRoot != "/w" {
		t.Fatalf("%+v", got)
	}
	if got := OptionsFrom(context.Background()); got.Entry != nil {
		t.Fatalf("%+v", got)
	}
}

func TestLearningJournalsWaitsForLead(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	h.change = "b.go"
	h.results = []runner.Result{{Done: false, Summary: "need a decision", Files: []string{}, Needs: []string{"decision:api-name"}}}
	out, err := h.run("implement")
	if err != nil || len(out.Needs) != 1 {
		t.Fatal(out, err)
	}
	rec := h.record()
	if rec.Done || len(rec.Needs) != 1 || rec.Needs[0] != "decision:api-name" || len(rec.Files) != 0 {
		t.Fatalf("%+v", rec)
	}
	dir := h.journal().TaskDir(rec.Task, rec.ID)
	for _, name := range []string{"before.patch", "result.patch"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatal(name, err)
		}
	}
}

func TestLearningSumsUsageAcrossLoginRetry(t *testing.T) {
	h := authHarness(t)
	h.learning(true)
	h.results = []runner.Result{{AuthFailed: true, Usage: runner.Usage{Input: 5, Write: 4, WriteHour: 1, Output: 1}}}
	h.auth.login = auth.LoggedIn
	out, err := h.run("implement")
	h.requireCalls(2)
	if err != nil || !out.Result.Done {
		t.Fatal(out, err)
	}
	rec := h.record()
	if rec.Input != 17 || rec.Output != 4 || rec.CacheWrite != 4 || rec.CacheWriteHour != 1 || !rec.Done {
		t.Fatalf("%+v", rec)
	}
	// The journal's cost carries the cache-write premium.
	want, priced := usage.Cost(h.cfg, rec.Agent, rec.Model, usage.Tokens{Input: 17, Write: 4, WriteHour: 1, Output: 4})
	plain, _ := usage.Cost(h.cfg, rec.Agent, rec.Model, usage.Tokens{Input: 17, Output: 4})
	if !priced || rec.USD != want || !(rec.USD > plain) {
		t.Fatalf("usd %v, want %v above plain input %v", rec.USD, want, plain)
	}
}

func TestLearningQuotaIsInfra(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	h.unavailable("claude")
	h.limited()
	out, err := h.runWith("debug_hard", Options{Features: diagnosis()})
	if err == nil {
		t.Fatal(out)
	}
	h.requireCalls(1)
	rec := h.record()
	if rec.Outcome != "infra" || rec.Done || rec.Entry != "codex/terra/medium" || rec.Agent != "codex" {
		t.Fatalf("%+v", rec)
	}
}

// A journal that cannot be written after the worker ran does not hide the answer.
func TestLearningErrorKeepsAnswer(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	inner := runWorker
	runWorker = func(ctx context.Context, s runner.Spec) (runner.Result, error) {
		path := filepath.Join(h.s.Dir, "journal", "delegations.ndjson")
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		return inner(ctx, s)
	}
	out, err := h.run("implement")
	if err == nil || !strings.HasPrefix(out.Line, "done=true") || !strings.Contains(err.Error(), "learning") {
		t.Fatal(out, err)
	}
}

// A wait for the lead decided before any worker starts is journalled, as an
// outcome that says nothing about the entry.
func TestLearningJournalsLeadFirstWait(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Edits, "high")
	h.learning(true)
	h.inventories(capability.Inventory{Agent: "codex"}, capability.Inventory{Agent: "claude", Items: []capability.Item{{Kind: "mcp", Name: "codegraph"}}})
	out, err := h.runWith("implement", Options{Needs: []string{"mcp:codegraph"}, Features: map[string]string{"output": "code"}})
	h.requireCalls(0)
	if err != nil || len(out.Needs) != 1 {
		t.Fatal(out, err)
	}
	rec := h.record()
	if rec.Outcome != journal.OutcomeLead || rec.Done || rec.RunHere || len(rec.Needs) != 1 || rec.Needs[0] != "mcp:codegraph" || rec.Input != 0 || rec.Output != 0 || rec.USD != 0 || rec.Class != "implement" {
		t.Fatalf("%+v", rec)
	}
	if _, err := os.Stat(h.journal().TaskDir(rec.Task, rec.ID)); !os.IsNotExist(err) {
		t.Fatal("files kept for a worker that never ran", err)
	}
}

// Attempt 0 hits a quota and the fallback is the lead's own agent: the record
// is the run-here of the final attempt, not the quota.
func TestLearningRunHereAfterQuotaRecordsFinalAttempt(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Edits, "high")
	h.learning(true)
	h.limited()
	h.results[0].Usage = runner.Usage{Input: 100, Output: 10}
	out, err := h.run("implement")
	if err != nil || out.RunHere == nil {
		t.Fatal(out, err)
	}
	h.requireCalls(1)
	rec := h.record()
	if !rec.RunHere || rec.Done || rec.Outcome != journal.OutcomeLead || rec.Input != 0 || rec.Output != 0 || rec.USD != 0 || rec.Agent != "claude" || route.Normalize("claude", rec.Model) != "sonnet" {
		t.Fatalf("%+v", rec)
	}
}

func TestLearningQuotaThenFallbackWorkerRecordsFallback(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	h.limited()
	h.results[0].Usage = runner.Usage{Input: 100, Output: 10}
	h.results = append(h.results, runner.Result{Done: true, Summary: "Completed.", Files: []string{}, Needs: []string{}, Usage: runner.Usage{Input: 12, Output: 3}})
	h.errs = append(h.errs, nil)
	out, err := h.runWith("implement", Options{Features: map[string]string{"output": "code"}})
	if err != nil {
		t.Fatal(out, err)
	}
	h.requireCalls(2)
	rec := h.record()
	if rec.Outcome != "" || !rec.Done || rec.Input != 12 || rec.Output != 3 || rec.Agent != "claude" || rec.Entry != "claude/opus/high" {
		t.Fatalf("%+v", rec)
	}
}

// A capture that fails stops the delegation before the worker, and no record
// claims a run.
func TestLearningCaptureFailureWritesNoRecord(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	put(t, filepath.Join(h.root, ".git", "index"), "not an index")
	out, err := h.run("implement")
	if err == nil || !strings.Contains(err.Error(), "learning") {
		t.Fatal(out, err)
	}
	h.requireCalls(0)
	if recs := h.records(); len(recs) != 0 {
		t.Fatalf("%+v", recs)
	}
}

// firstDebug runs debug once and returns its record, the delegation to leave.
func (h *harness) firstDebug() journal.Delegation {
	h.t.Helper()
	if out, err := h.runWith("debug_hard", Options{Features: diagnosis()}); err != nil {
		h.t.Fatal(out, err)
	}
	return h.records()[0]
}

// leftRecord appends a delegation of the open task, as an earlier run would.
func (h *harness) leftRecord(d journal.Delegation) journal.Delegation {
	h.t.Helper()
	task, ok, err := h.journal().CurrentTask("lead-123456")
	if err != nil || !ok {
		h.t.Fatal(task, ok, err)
	}
	d.Task, d.Lead, d.Role, d.Class, d.RuleClass, d.Ladder = task.ID, "lead-123456", "debug_hard", "debug", "debug", "debug"
	d.Features = diagnosis()
	if err := h.journal().Append("delegations.ndjson", d); err != nil {
		h.t.Fatal(err)
	}
	return d
}

func TestEscalationMovesToNextEntry(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	first := h.firstDebug()
	if first.Entry != "codex/terra/medium" {
		t.Fatalf("%+v", first)
	}
	out, err := h.runWith("debug_hard", Options{Features: diagnosis(), EscalateFrom: first.ID, EscalateReason: "not_green"})
	if err != nil {
		t.Fatal(out, err)
	}
	h.requireCalls(2)
	if r := h.calls[1].Route; r.Agent != "codex" || route.Normalize("codex", r.Model) != "sol" || r.Effort != "medium" {
		t.Fatal(r)
	}
	rec := h.records()[1]
	if rec.Why != "escalation" || rec.EscalatedFrom != first.ID || rec.EscalationReason != "not_green" || rec.Entry != "codex/sol/medium" || rec.Step != 2 {
		t.Fatalf("%+v", rec)
	}
}

func TestEscalationReusesFeatures(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	first := h.firstDebug()
	if out, err := h.runWith("debug_hard", Options{EscalateFrom: first.ID, EscalateReason: "no_lines"}); err != nil {
		t.Fatal(out, err)
	}
	rec := h.records()[1]
	if rec.Class != "debug" || rec.Features["output"] != "diagnosis" || rec.Entry != "codex/sol/medium" {
		t.Fatalf("%+v", rec)
	}
}

func TestEscalationNeedsKnownReason(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	first := h.firstDebug()
	_, err := h.runWith("debug_hard", Options{EscalateFrom: first.ID, EscalateReason: "tired"})
	if err == nil || !strings.Contains(err.Error(), "out_of_turns") {
		t.Fatal(err)
	}
	h.requireCalls(1)
}

func TestEscalationFromLastEntryRefused(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	left := h.leftRecord(journal.Delegation{ID: "task-last", Entry: "claude/opus/high", Why: "escalation"})
	_, err := h.runWith("debug_hard", Options{EscalateFrom: left.ID, EscalateReason: "inconsistent"})
	if err == nil || !strings.Contains(err.Error(), "class debug has no entry after claude/opus/high") {
		t.Fatal(err)
	}
	h.requireCalls(0)
}

func TestEscalationUnknownDelegation(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	_, err := h.runWith("debug_hard", Options{EscalateFrom: "task-nope", EscalateReason: "no_repro"})
	if err == nil || !strings.Contains(err.Error(), `delegation "task-nope" is not in the open task`) {
		t.Fatal(err)
	}
	h.requireCalls(0)
}

func TestEscalationFromInfraRefused(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	h.firstDebug()
	left := h.leftRecord(journal.Delegation{ID: "task-infra", Entry: "codex/terra/medium", Why: "prior", Outcome: journal.OutcomeInfra})
	_, err := h.runWith("debug_hard", Options{EscalateFrom: left.ID, EscalateReason: "out_of_turns"})
	if err == nil || !strings.Contains(err.Error(), "provider limit") {
		t.Fatal(err)
	}
	h.requireCalls(1)
}

func TestEscalationFromPersonChoiceRefused(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	h.firstDebug()
	left := h.leftRecord(journal.Delegation{ID: "task-person", Entry: "codex/terra/medium", Why: "person"})
	_, err := h.runWith("debug_hard", Options{EscalateFrom: left.ID, EscalateReason: "not_green"})
	if err == nil || !strings.Contains(err.Error(), "explicit choice") {
		t.Fatal(err)
	}
	h.requireCalls(1)
	// With --use the person has agreed to the change.
	out, err := h.runWith("debug_hard", Options{EscalateFrom: left.ID, EscalateReason: "not_green", Entry: target("claude/opus/high"), Person: true})
	if err != nil {
		t.Fatal(out, err)
	}
	h.requireCalls(2)
	rec := h.records()[len(h.records())-1]
	if rec.EscalatedFrom != left.ID || rec.Entry != "claude/opus/high" || rec.Why != "person" {
		t.Fatalf("%+v", rec)
	}
}

// A run on the role's own target or one that never reached a worker says
// nothing about a ladder entry, so there is nothing to escalate from.
func TestEscalationNeedsLadderRun(t *testing.T) {
	for _, c := range []struct {
		left journal.Delegation
		want string
	}{
		{journal.Delegation{ID: "task-role", Why: "role"}, "not on a ladder entry"},
		{journal.Delegation{ID: "task-lead", Entry: "codex/terra/medium", Why: "prior", Outcome: journal.OutcomeLead}, "before a worker ran"},
	} {
		h := newHarness(t)
		h.learning(true)
		left := h.leftRecord(c.left)
		_, err := h.runWith("debug_hard", Options{EscalateFrom: left.ID, EscalateReason: "no_repro"})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatal(c.left.ID, err)
		}
		h.requireCalls(0)
	}
}
