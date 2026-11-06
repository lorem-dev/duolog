package profile

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/delegate"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/runner"
	"github.com/lorem-dev/duolog/internal/store"
)

func TestReviewJudgeScaleAlwaysRewardsQuality(t *testing.T) {
	task, _ := JudgeTask(".duolog/judge/v", config.Class{Rubric: []string{"false claims (count)"}}, nil, Side{}, Side{})
	for _, want := range []string{"3 is best", "0 is worst", "fewer is better"} {
		if !strings.Contains(string(task), want) {
			t.Fatalf("missing %q: %s", want, task)
		}
	}
}

func TestReviewNonQualityRecordsCannotReplay(t *testing.T) {
	for _, flag := range []string{`"run_here":true`, `"interrupted":true`, `"needs":["question"]`, `"outcome":"lead"`} {
		r := journal.Delegation{ID: "d", Class: "debug", Task: "t"}
		if err := json.Unmarshal([]byte("{"+flag+"}"), &r); err != nil {
			t.Fatal(err)
		}
		if _, err := selectRecords([]journal.Delegation{r}, r.ID, false); err == nil {
			t.Fatalf("replayed %s", flag)
		}
	}
}

func TestReviewReplayLabelsUseTheEffortThatRan(t *testing.T) {
	f := newFixture(t)
	f.rec.Entry = "codex/terra/high"
	f.rec.EffortCapped, f.rec.Effort = true, "medium"
	rewriteRecords(t, f, f.rec)
	f.fake(t, func(ctx context.Context, _ *config.Config, _ *store.Store, _, _, _ string) (delegate.Outcome, error) {
		r := journal.Delegation{ID: "replayed", ProfileOf: delegate.OptionsFrom(ctx).ReplayOf, Entry: "codex/sol/medium", Agent: "codex", Model: "sol", Effort: "medium", EffortCapped: true}
		if err := f.j.Append("delegations.ndjson", r); err != nil {
			t.Fatal(err)
		}
		return delegate.Outcome{Result: runner.Result{Done: true}}, nil
	})
	r := f.run(t, Options{})
	if r.Original.Entry != "codex/terra/medium" || r.Profile.Entry != "codex/sol/medium" {
		t.Fatal(r.Original.Entry, r.Profile.Entry)
	}
}

func TestReviewDamagedGitFileCannotReachMainCheckout(t *testing.T) {
	for _, kind := range []string{"removed", "redirected"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			writeTest(t, filepath.Join(f.root, "base.txt"), []byte("person's unsaved work\n"))
			writeTest(t, filepath.Join(f.s.Dir, "keep.txt"), []byte("journal sentinel"))
			before := gitTest(t, f.root, "status", "--porcelain")
			t.Setenv("DUOLOG_TEST_CORRUPT_GIT", kind)
			t.Setenv("DUOLOG_TEST_MAIN_ROOT", f.root)
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			c := f.cfg.Learning.Classes[f.rec.Class]
			c.Checks = []string{exe + " -test.run=^TestReviewCorruptWorktreeCheck$"}
			f.cfg.Learning.Classes[f.rec.Class] = c
			r, _ := Run(context.Background(), f.cfg, f.s, f.rec.ID, Options{})
			if len(r.Profile.Checks) != 1 || r.Profile.Checks[0].Exit != 0 {
				t.Fatal("corruption check did not execute", r.Profile.Checks)
			}
			if got := gitTest(t, f.root, "status", "--porcelain"); got != before {
				t.Fatalf("main checkout changed: %s -> %s", before, got)
			}
			if !bytes.Equal(readTest(t, filepath.Join(f.root, "base.txt")), []byte("person's unsaved work\n")) || string(readTest(t, filepath.Join(f.s.Dir, "keep.txt"))) != "journal sentinel" {
				t.Fatal("main files changed")
			}
		})
	}
}

func TestReviewCorruptWorktreeCheck(t *testing.T) {
	kind := os.Getenv("DUOLOG_TEST_CORRUPT_GIT")
	if kind == "" {
		return
	}
	root := os.Getenv("DUOLOG_TEST_MAIN_ROOT")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(root, ".duolog", WorktreeDir))
	if err != nil {
		t.Fatal(err)
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil || cwd != want {
		t.Fatal("helper must run in the temporary profile worktree")
	}
	path := filepath.Join(cwd, ".git")
	if kind == "removed" {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := os.WriteFile(path, []byte("gitdir: "+filepath.Join(root, ".git")+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func rewriteRecords(t *testing.T, f *fixture, records ...journal.Delegation) {
	t.Helper()
	var data bytes.Buffer
	for _, rec := range records {
		if err := json.NewEncoder(&data).Encode(rec); err != nil {
			t.Fatal(err)
		}
	}
	writeTest(t, filepath.Join(f.j.Dir, "delegations.ndjson"), data.Bytes())
}

func recordedChain(t *testing.T) (*fixture, journal.Delegation) {
	t.Helper()
	f := newFixture(t)
	var err error
	f.rec.BeforeTree, err = journal.Capture(f.root)
	if err != nil {
		t.Fatal(err)
	}
	writeTest(t, filepath.Join(f.root, "plan.txt"), []byte("original plan\n"))
	writeTest(t, filepath.Join(f.root, "base.txt"), []byte("step one\n"))
	f.rec.AfterTree, err = journal.Capture(f.root)
	if err != nil {
		t.Fatal(err)
	}
	patch, err := journal.Diff(f.root, f.rec.BeforeTree, f.rec.AfterTree)
	if err != nil {
		t.Fatal(err)
	}
	writeTest(t, filepath.Join(f.j.TaskDir(f.rec.Task, f.rec.ID), "result.patch"), patch)
	second := f.rec
	second.ID, second.Step, second.Role = "d2", 2, "implement_complex"
	writeTest(t, filepath.Join(f.root, "base.txt"), []byte("lead edited the base\n"))
	writeTest(t, filepath.Join(f.root, "lead.txt"), []byte("lead input\n"))
	second.BeforeTree, err = journal.Capture(f.root)
	if err != nil {
		t.Fatal(err)
	}
	writeTest(t, filepath.Join(f.root, "work.txt"), []byte("original result\n"))
	writeTest(t, filepath.Join(f.root, "base.txt"), []byte("original final base\n"))
	second.AfterTree, err = journal.Capture(f.root)
	if err != nil {
		t.Fatal(err)
	}
	dir := f.j.TaskDir(second.Task, second.ID)
	patch, err = journal.Diff(f.root, second.Base, second.BeforeTree)
	if err != nil {
		t.Fatal(err)
	}
	writeTest(t, filepath.Join(dir, "before.patch"), patch)
	patch, err = journal.Diff(f.root, second.BeforeTree, second.AfterTree)
	if err != nil {
		t.Fatal(err)
	}
	writeTest(t, filepath.Join(dir, "result.patch"), patch)
	writeTest(t, filepath.Join(dir, "task.md"), []byte("Use plan.txt and lead.txt for the implementation."))
	writeTest(t, filepath.Join(dir, "answer.json"), []byte(`{"summary":"original implementation"}`))
	rewriteRecords(t, f, f.rec, second)
	return f, second
}

func TestReviewChainReplaysLeadDeltaAndLastOriginalTree(t *testing.T) {
	f, second := recordedChain(t)
	calls := 0
	f.fake(t, func(ctx context.Context, _ *config.Config, _ *store.Store, _, _, _ string) (delegate.Outcome, error) {
		calls++
		wt := delegate.OptionsFrom(ctx).WorkRoot
		if calls == 1 {
			writeTest(t, filepath.Join(wt, "plan.txt"), []byte("alternative plan\n"))
			writeTest(t, filepath.Join(wt, "base.txt"), []byte("step one\n"))
		} else {
			if string(readTest(t, filepath.Join(wt, "plan.txt"))) != "alternative plan\n" || string(readTest(t, filepath.Join(wt, "base.txt"))) != "lead edited the base\n" || string(readTest(t, filepath.Join(wt, "lead.txt"))) != "lead input\n" {
				t.Fatal("next step did not receive the replay and lead inputs")
			}
			writeTest(t, filepath.Join(wt, "work.txt"), []byte("profile result\n"))
			writeTest(t, filepath.Join(wt, "base.txt"), []byte("profile final base\n"))
		}
		return delegate.Outcome{Result: runner.Result{Done: true, Summary: "profile step"}}, nil
	})
	r := f.run(t, Options{Chain: true})
	want, err := journal.Diff(f.root, f.rec.BeforeTree, second.AfterTree)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !bytes.Equal(r.Original.Patch, want) || !bytes.Contains(r.Profile.Patch, []byte("alternative plan")) || !bytes.Contains(r.Profile.Patch, []byte("profile result")) {
		t.Fatal(calls, string(r.Original.Patch), string(r.Profile.Patch))
	}
}

func TestReviewChainRefusesMissingHistoryBeforeSpending(t *testing.T) {
	f, second := recordedChain(t)
	second.BeforeTree = ""
	rewriteRecords(t, f, f.rec, second)
	calls := 0
	f.fake(t, func(context.Context, *config.Config, *store.Store, string, string, string) (delegate.Outcome, error) {
		calls++
		return delegate.Outcome{}, nil
	})
	_, err := Run(context.Background(), f.cfg, f.s, f.rec.ID, Options{Chain: true})
	if err == nil || !strings.Contains(err.Error(), "chain") || calls != 0 {
		t.Fatal(calls, err)
	}
}

func TestReviewChainRefusesEmbeddedOriginalAnswerBeforeSpending(t *testing.T) {
	f, second := recordedChain(t)
	writeTest(t, filepath.Join(f.j.TaskDir(second.Task, second.ID), "task.md"), []byte("Implement this recorded plan: original answer"))
	calls := 0
	f.fake(t, func(context.Context, *config.Config, *store.Store, string, string, string) (delegate.Outcome, error) {
		calls++
		return delegate.Outcome{}, nil
	})
	_, err := Run(context.Background(), f.cfg, f.s, f.rec.ID, Options{Chain: true})
	if err == nil || !strings.Contains(err.Error(), "chain") || calls != 0 {
		t.Fatal(calls, err)
	}
}

func TestReviewChainRefusesPastedOriginalFileBeforeSpending(t *testing.T) {
	f, second := recordedChain(t)
	writeTest(t, filepath.Join(f.j.TaskDir(second.Task, second.ID), "task.md"), []byte("Implement this pasted plan: original plan"))
	calls := 0
	f.fake(t, func(context.Context, *config.Config, *store.Store, string, string, string) (delegate.Outcome, error) {
		calls++
		return delegate.Outcome{}, nil
	})
	_, err := Run(context.Background(), f.cfg, f.s, f.rec.ID, Options{Chain: true})
	if err == nil || !strings.Contains(err.Error(), "chain") || calls != 0 {
		t.Fatal(calls, err)
	}
}
