package profile

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/run"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/store"
)

type fixture struct {
	root string
	cfg  *config.Config
	s    *store.Store
	j    *journal.Journal
	rec  journal.Delegation
	deps Deps
}

// execCheck stands for kit.RunCheck in tests that need a real check to run.
func execCheck(ctx context.Context, dir, command string) int {
	args := strings.Fields(command)
	if len(args) == 0 {
		return -1
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	if err := cmd.Run(); err == nil {
		return 0
	}
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode()
	}
	return -1
}

func gitTest(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root, "-c", "core.autocrlf=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeTest(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func readTest(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	writeTest(t, filepath.Join(root, "base.txt"), []byte("base\n"))
	gitTest(t, root, "add", "base.txt")
	gitTest(t, root, "-c", "user.name=test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "base")
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	j, err := journal.Open(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := configfile.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Learning.Enabled = true
	r := journal.Delegation{ID: "d1", Task: "t1", Step: 1, Role: "debug_hard", Class: "debug", Ladder: "debug", Entry: "codex/terra/medium", Agent: "codex", Model: "terra", Effort: "medium", USD: 0.2, Base: gitTest(t, root, "rev-parse", "HEAD")}
	writeTest(t, filepath.Join(root, "x.txt"), []byte("before\n"))
	before, err := journal.Capture(root)
	if err != nil {
		t.Fatal(err)
	}
	patch, err := journal.Diff(root, r.Base, before)
	if err != nil {
		t.Fatal(err)
	}
	dir := j.TaskDir(r.Task, r.ID)
	writeTest(t, filepath.Join(dir, "before.patch"), patch)
	writeTest(t, filepath.Join(dir, "result.patch"), nil)
	writeTest(t, filepath.Join(dir, "task.md"), []byte("# Task\n\nReplay this recorded task.\n"))
	writeTest(t, filepath.Join(dir, "answer.json"), []byte(`{"done":true,"summary":"original answer","files":[],"needs":[]}`))
	if err := j.Append("delegations.ndjson", r); err != nil {
		t.Fatal(err)
	}
	f := &fixture{root: root, cfg: cfg, s: s, j: j, rec: r,
		deps: Deps{Rand: func() bool { return true }, RunCheck: execCheck}}
	f.fake(t, func(context.Context, *config.Config, *store.Store, string, string, string) (delegate.Outcome, error) {
		return delegate.Outcome{Result: run.Result{Done: true, Summary: "profile answer"}}, nil
	})
	return f
}

type optionsKey struct{}

// optionsFrom is the options of the request that a fake delegation received.
func optionsFrom(ctx context.Context) delegate.Options {
	o, _ := ctx.Value(optionsKey{}).(delegate.Options)
	return o
}

// fake replaces the fixture's Delegate with fn, which gets the request's
// fields as arguments and its options from optionsFrom(ctx).
func (f *fixture) fake(t *testing.T, fn func(context.Context, *config.Config, *store.Store, string, string, string) (delegate.Outcome, error)) {
	t.Helper()
	f.deps.Delegate = func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		return fn(context.WithValue(ctx, optionsKey{}, r.Options), r.Config, &store.Store{Dir: r.StateDir}, r.Role, r.TaskPath, r.Thread)
	}
}

func (f *fixture) run(t *testing.T, opts Options) Replay {
	t.Helper()
	if opts.Entry.Agent == "" {
		opts.Entry = config.Target{Agent: "codex", Model: "sol", Effort: "high"}
	}
	r, err := Run(context.Background(), f.deps, f.cfg, f.s, f.rec.ID, opts)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestReplayWorktreeAtBaseWithBeforePatch(t *testing.T) {
	f := newFixture(t)
	f.fake(t, func(ctx context.Context, _ *config.Config, s *store.Store, _, task, _ string) (delegate.Outcome, error) {
		wt := optionsFrom(ctx).WorkRoot
		if wt != filepath.Join(f.s.Dir, WorktreeDir) || s.Dir != f.s.Dir {
			t.Fatalf("work root %q, store %v", wt, s)
		}
		if head := gitTest(t, wt, "rev-parse", "HEAD"); head != f.rec.Base {
			t.Fatal(head)
		}
		if string(readTest(t, filepath.Join(wt, "x.txt"))) != "before\n" {
			t.Fatal("before patch not applied")
		}
		if task != filepath.Join(wt, ".duolog", "task.md") || !bytes.Equal(readTest(t, task), readTest(t, filepath.Join(f.j.TaskDir(f.rec.Task, f.rec.ID), "task.md"))) {
			t.Fatal("task copy differs")
		}
		return delegate.Outcome{}, nil
	})
	f.run(t, Options{})
}

func TestReplayPassesEntryAndReplayOf(t *testing.T) {
	f := newFixture(t)
	want := config.Target{Agent: "codex", Model: "sol", Effort: "high"}
	f.fake(t, func(ctx context.Context, _ *config.Config, _ *store.Store, role, _, _ string) (delegate.Outcome, error) {
		o := optionsFrom(ctx)
		if role != "debug_hard" || o.Entry == nil || *o.Entry != want || o.ReplayOf != f.rec.ID || o.Lead != "lead-test" || !o.AllowManualModel {
			t.Fatalf("role %q, options %+v", role, o)
		}
		if got, _ := os.Getwd(); got == o.WorkRoot {
			t.Fatal("process directory changed")
		}
		return delegate.Outcome{}, nil
	})
	r := f.run(t, Options{Entry: want, Lead: "lead-test", AllowManualModel: true})
	if r.Task != f.rec.Task || r.Class != f.rec.Class || r.Ladder != f.rec.Ladder || r.Role != f.rec.Role || r.Original.Entry != f.rec.Entry || r.Profile.Entry != "codex/sol/high" {
		t.Fatalf("%+v", r)
	}
}

func TestReplayProfilePatchHoldsOnlyReplayChanges(t *testing.T) {
	f := newFixture(t)
	f.fake(t, func(ctx context.Context, _ *config.Config, _ *store.Store, _, _, _ string) (delegate.Outcome, error) {
		writeTest(t, filepath.Join(optionsFrom(ctx).WorkRoot, "y.txt"), []byte("replayed\n"))
		return delegate.Outcome{Result: run.Result{Done: true, Summary: "made y"}}, nil
	})
	r := f.run(t, Options{})
	if !bytes.Contains(r.Profile.Patch, []byte("y.txt")) || bytes.Contains(r.Profile.Patch, []byte("x.txt")) || r.Profile.Summary != "made y" || r.Original.Summary != "original answer" {
		t.Fatalf("%+v", r)
	}
	for n := 1; n <= 2; n++ {
		if n == 2 {
			r = f.run(t, Options{})
		}
		dir := filepath.Join(f.j.TaskDir(f.rec.Task, f.rec.ID), "profile-"+strconv.Itoa(n))
		if !bytes.Equal(readTest(t, filepath.Join(dir, "side.patch")), r.Profile.Patch) || !bytes.Contains(readTest(t, filepath.Join(dir, "answer.json")), []byte("made y")) {
			t.Fatal("profile artifacts differ")
		}
	}
}

func TestReplayAppliesBinaryBeforePatch(t *testing.T) {
	f := newFixture(t)
	want := []byte{0, 1, 255, 0, 4}
	writeTest(t, filepath.Join(f.root, "binary.dat"), want)
	tree, err := journal.Capture(f.root)
	if err != nil {
		t.Fatal(err)
	}
	patch, err := journal.Diff(f.root, f.rec.Base, tree)
	if err != nil {
		t.Fatal(err)
	}
	writeTest(t, filepath.Join(f.j.TaskDir(f.rec.Task, f.rec.ID), "before.patch"), patch)
	f.fake(t, func(ctx context.Context, _ *config.Config, _ *store.Store, _, _, _ string) (delegate.Outcome, error) {
		if !bytes.Equal(readTest(t, filepath.Join(optionsFrom(ctx).WorkRoot, "binary.dat")), want) {
			t.Fatal("binary bytes differ")
		}
		return delegate.Outcome{}, nil
	})
	f.run(t, Options{})
}

func TestReplayLeavesMainCheckoutUntouched(t *testing.T) {
	f := newFixture(t)
	writeTest(t, filepath.Join(f.root, "base.txt"), []byte("staged\n"))
	gitTest(t, f.root, "add", "base.txt")
	writeTest(t, filepath.Join(f.root, "base.txt"), []byte("unstaged\n"))
	status := gitTest(t, f.root, "status", "--porcelain")
	index := readTest(t, filepath.Join(f.root, ".git", "index"))
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	f.run(t, Options{})
	if got := gitTest(t, f.root, "status", "--porcelain"); got != status {
		t.Fatalf("status changed: %q -> %q", status, got)
	}
	if got := gitTest(t, f.root, "worktree", "list"); len(strings.Split(got, "\n")) != 1 {
		t.Fatal(got)
	}
	if !bytes.Equal(index, readTest(t, filepath.Join(f.root, ".git", "index"))) || string(readTest(t, filepath.Join(f.root, "base.txt"))) != "unstaged\n" {
		t.Fatal("main checkout changed")
	}
	if got, err := os.Getwd(); err != nil || got != cwd {
		t.Fatal(got, err)
	}
}

func TestChecksRunOnBothSides(t *testing.T) {
	f := newFixture(t)
	c := f.cfg.Learning.Classes[f.rec.Class]
	c.Checks = []string{"git --version", "no-such-program-x"}
	f.cfg.Learning.Classes[f.rec.Class] = c
	r := f.run(t, Options{})
	for _, side := range []Side{r.Original, r.Profile} {
		if len(side.Checks) != 2 || side.Checks[0].Exit != 0 || side.Checks[1].Exit != -1 || side.Checks[0].Command != c.Checks[0] {
			t.Fatalf("%+v", side.Checks)
		}
	}
}

func TestChecksSeeReconstructedOriginalAndReplay(t *testing.T) {
	f := newFixture(t)
	before, err := journal.Capture(f.root)
	if err != nil {
		t.Fatal(err)
	}
	writeTest(t, filepath.Join(f.root, "base.txt"), []byte("original change\n"))
	after, err := journal.Capture(f.root)
	if err != nil {
		t.Fatal(err)
	}
	patch, err := journal.Diff(f.root, before, after)
	if err != nil {
		t.Fatal(err)
	}
	writeTest(t, filepath.Join(f.j.TaskDir(f.rec.Task, f.rec.ID), "result.patch"), patch)
	c := f.cfg.Learning.Classes[f.rec.Class]
	c.Checks = []string{"git diff --exit-code -- base.txt"}
	f.cfg.Learning.Classes[f.rec.Class] = c
	f.fake(t, func(ctx context.Context, _ *config.Config, _ *store.Store, _, _, _ string) (delegate.Outcome, error) {
		writeTest(t, filepath.Join(optionsFrom(ctx).WorkRoot, "y.txt"), []byte("profile only\n"))
		return delegate.Outcome{Result: run.Result{Summary: "replay"}}, nil
	})
	r := f.run(t, Options{})
	if r.Profile.Checks[0].Exit != 0 || r.Original.Checks[0].Exit != 1 {
		t.Fatalf("original %+v, profile %+v", r.Original.Checks, r.Profile.Checks)
	}
	if !bytes.Equal(r.Original.Patch, patch) || bytes.Contains(r.Original.Patch, []byte("y.txt")) {
		t.Fatalf("original patch %s", r.Original.Patch)
	}
}

// TestProfileChecksUseRunCheck: the exit code of each check comes from the
// RunCheck of Deps, and once ctx ends the later checks fail with -1 without
// RunCheck being called for them.
func TestProfileChecksUseRunCheck(t *testing.T) {
	t.Parallel()
	var ran []string
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := Deps{RunCheck: func(_ context.Context, dir, command string) int {
		ran = append(ran, dir+"|"+command)
		if command == "go test ./..." {
			return 3
		}
		cancel()
		return 0
	}}
	checks := RunChecks(ctx, d, "wt", []string{"go test ./...", "go vet ./...", "never ran"})
	if len(checks) != 3 || checks[0].Exit != 3 || checks[1].Exit != 0 || checks[2].Exit != -1 || checks[2].Command != "never ran" {
		t.Fatalf("checks %+v", checks)
	}
	if !reflect.DeepEqual(ran, []string{"wt|go test ./...", "wt|go vet ./..."}) {
		t.Fatalf("RunCheck calls %v", ran)
	}
}

// TestProfileUsesDelegate: the judge's Delegate sees the two sides in the
// order that Rand of Deps gives. It cannot run in parallel: judgeFixture
// changes the working directory.
func TestProfileUsesDelegate(t *testing.T) {
	f, r := judgeFixture(t)
	stubShuffle(f, false)
	var first []byte
	f.fake(t, func(_ context.Context, _ *config.Config, _ *store.Store, _, path, _ string) (delegate.Outcome, error) {
		first = readTest(t, filepath.Join(filepath.Dir(path), "a.patch"))
		return delegate.Outcome{Result: run.Result{Done: true, Summary: judgeSummary}}, nil
	})
	if _, err := Judge(context.Background(), f.deps, f.cfg, f.s, r, delegate.Options{}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, r.Profile.Patch) {
		t.Fatalf("side A %q, want the profile's patch %q", first, r.Profile.Patch)
	}
}

func TestChainReplaysFollowingSteps(t *testing.T) {
	f := newFixture(t)
	tree, err := journal.Capture(f.root)
	if err != nil {
		t.Fatal(err)
	}
	f.rec.BeforeTree, f.rec.AfterTree = tree, tree
	rewriteRecords(t, f, f.rec)
	second := f.rec
	second.ID, second.Step, second.Role = "d2", 2, "implement_complex"
	// Routing had fallen back: the later step must use what actually ran.
	second.Entry, second.Agent, second.Model, second.Effort = "codex/sol/high", "claude", "opus", "high"
	second.USD = 0.3
	profiled := f.rec
	profiled.ID, profiled.ProfileOf, profiled.Step = "old-profile", f.rec.ID, 2
	infra := f.rec
	infra.ID, infra.Step, infra.Outcome, infra.Class = "infra", 2, journal.OutcomeInfra, "docs"
	otherTask := f.rec
	otherTask.ID, otherTask.Task, otherTask.Step, otherTask.Class = "other-task", "t2", 2, "docs"
	stop := second
	stop.ID, stop.Step, stop.Class = "stop", 3, "docs"
	later := second
	later.ID, later.Step = "later", 4
	// Deliberately append out of step order.
	for _, r := range []journal.Delegation{later, profiled, infra, otherTask, stop, second} {
		if err := f.j.Append("delegations.ndjson", r); err != nil {
			t.Fatal(err)
		}
	}
	dir := f.j.TaskDir(second.Task, second.ID)
	writeTest(t, filepath.Join(dir, "task.md"), []byte("second task"))
	writeTest(t, filepath.Join(dir, "before.patch"), readTest(t, filepath.Join(f.j.TaskDir(f.rec.Task, f.rec.ID), "before.patch")))
	writeTest(t, filepath.Join(dir, "result.patch"), nil)
	writeTest(t, filepath.Join(dir, "answer.json"), []byte(`{"summary":"second answer"}`))
	var calls []string
	f.fake(t, func(ctx context.Context, _ *config.Config, _ *store.Store, role, task, _ string) (delegate.Outcome, error) {
		o := optionsFrom(ctx)
		calls = append(calls, o.ReplayOf)
		want := config.Target{Agent: "codex", Model: "sol", Effort: "high"}
		if len(calls) == 2 {
			want = config.Target{Agent: "claude", Model: "opus", Effort: "high"}
			if role != second.Role || string(readTest(t, task)) != "second task" || string(readTest(t, filepath.Join(o.WorkRoot, "y.txt"))) != "first\n" {
				t.Fatal("chain did not retain prior work")
			}
		}
		if o.Entry == nil || *o.Entry != want {
			t.Fatalf("entry %+v, want %+v", o.Entry, want)
		}
		writeTest(t, filepath.Join(o.WorkRoot, "y.txt"), []byte("first\n"))
		r := f.rec
		r.ID, r.ProfileOf, r.USD = "replay-"+o.ReplayOf, o.ReplayOf, 0.1
		if err := f.j.Append("delegations.ndjson", r); err != nil {
			t.Fatal(err)
		}
		return delegate.Outcome{Result: run.Result{Summary: o.ReplayOf}}, nil
	})
	r := f.run(t, Options{Chain: true})
	if !reflect.DeepEqual(calls, []string{"d1", "d2"}) || !reflect.DeepEqual(r.Delegations, calls) || r.Original.USD != 0.5 || r.Profile.USD != 0.2 || !strings.Contains(r.Profile.Summary, "d2") || !strings.Contains(r.Original.Summary, "second answer") {
		t.Fatalf("calls %v, replay %+v", calls, r)
	}
}

func TestReplayRefusesWithoutBase(t *testing.T) {
	for _, missing := range []string{"base", "before.patch"} {
		t.Run(missing, func(t *testing.T) {
			f := newFixture(t)
			if missing == "base" {
				f.rec.Base = ""
				if err := os.Remove(filepath.Join(f.j.Dir, "delegations.ndjson")); err != nil {
					t.Fatal(err)
				}
				if err := f.j.Append("delegations.ndjson", f.rec); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Remove(filepath.Join(f.j.TaskDir(f.rec.Task, f.rec.ID), "before.patch")); err != nil {
				t.Fatal(err)
			}
			_, err := Run(context.Background(), f.deps, f.cfg, f.s, f.rec.ID, Options{})
			want := "delegation d1 has no base commit; it was not in a Git repository"
			if err == nil || err.Error() != want {
				t.Fatal(err)
			}
		})
	}
}

func TestReplayRemovesWorktreeOnError(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed", true: "interrupted"}[interrupted], func(t *testing.T) {
			f := newFixture(t)
			status := gitTest(t, f.root, "status", "--porcelain")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := errors.New("worker failed")
			f.fake(t, func(ctx context.Context, _ *config.Config, _ *store.Store, _, _, _ string) (delegate.Outcome, error) {
				writeTest(t, filepath.Join(optionsFrom(ctx).WorkRoot, "y.txt"), []byte("partial"))
				if interrupted {
					cancel()
					return delegate.Outcome{}, context.Canceled
				}
				return delegate.Outcome{}, want
			})
			_, err := Run(ctx, f.deps, f.cfg, f.s, f.rec.ID, Options{})
			if interrupted {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(f.s.Dir, WorktreeDir)); !os.IsNotExist(err) {
				t.Fatal("worktree remains", err)
			}
			if got := gitTest(t, f.root, "status", "--porcelain"); got != status {
				t.Fatalf("main status %q -> %q", status, got)
			}
			if got := gitTest(t, f.root, "worktree", "list"); len(strings.Split(got, "\n")) != 1 {
				t.Fatal(got)
			}
		})
	}
}

func TestReplayCleansStaleWorktree(t *testing.T) {
	for _, registered := range []bool{true, false} {
		t.Run(map[bool]string{true: "registered", false: "unregistered"}[registered], func(t *testing.T) {
			f := newFixture(t)
			wt := filepath.Join(f.s.Dir, WorktreeDir)
			if registered {
				gitTest(t, f.root, "worktree", "add", "--detach", wt, f.rec.Base)
			}
			writeTest(t, filepath.Join(wt, "stale.txt"), []byte("stale"))
			f.fake(t, func(ctx context.Context, _ *config.Config, _ *store.Store, _, _, _ string) (delegate.Outcome, error) {
				if _, err := os.Stat(filepath.Join(optionsFrom(ctx).WorkRoot, "stale.txt")); !os.IsNotExist(err) {
					t.Fatal("stale content remains", err)
				}
				return delegate.Outcome{}, nil
			})
			f.run(t, Options{})
		})
	}
}

func TestReplayNamesWorktreeWhenTrustRefuses(t *testing.T) {
	f := newFixture(t)
	f.fake(t, func(context.Context, *config.Config, *store.Store, string, string, string) (delegate.Outcome, error) {
		return delegate.Outcome{}, errors.New("Codex hooks untrusted; open /hooks to trust them")
	})
	_, err := Run(context.Background(), f.deps, f.cfg, f.s, f.rec.ID, Options{})
	if err == nil || !strings.Contains(err.Error(), "profiling worktree: "+filepath.Join(f.s.Dir, WorktreeDir)+"; open /hooks there once") {
		t.Fatal(err)
	}
}

func TestReplayWorkerMayWriteInsideWorktree(t *testing.T) {
	f := newFixture(t)
	f.fake(t, func(ctx context.Context, _ *config.Config, _ *store.Store, _, _, _ string) (delegate.Outcome, error) {
		wt := optionsFrom(ctx).WorkRoot
		work := project.WorkRoot(wt, f.root)
		for _, item := range []struct{ path, want string }{{"y.txt", gate.Allow}, {".duolog/task.md", gate.Deny}} {
			a := gate.Action{Kind: "write", Target: gate.RelativeTarget(work, wt, filepath.Join(wt, item.path)), Role: "implement_complex"}
			if d := gate.Evaluate(f.cfg.Policy, gate.LeadRules{}, mode.Full, a); d.Kind != item.want {
				t.Fatalf("%+v: %+v", a, d)
			}
		}
		return delegate.Outcome{}, nil
	})
	f.run(t, Options{})
}
