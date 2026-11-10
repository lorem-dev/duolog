package calibration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/journal"
)

func TestResumePreservesHeldWorkerTree(t *testing.T) {
	// Kills resetting a running cell before checking its live reservation.
	f := newRunFixture(t, []string{"t1"}, []string{"codex/sol/medium"})
	tree := t.TempDir()
	marker := filepath.Join(tree, "worker.txt")
	if err := os.WriteFile(marker, []byte("work"), 0600); err != nil {
		t.Fatal(err)
	}
	id := f.run.Cells[0].ID
	f.run.Cells[0].State, f.run.Cells[0].Tree = CellRunning, tree
	f.run.Receipts = map[string]string{id: id + ".1"}
	f.runner.Accounts = &fakeAccounts{held: map[string]bool{f.runner.receipt(id + ".1"): true}}
	o, err := f.runner.Resume(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if o.Stopped != stopHeld || len(f.calls) != 0 {
		t.Errorf("resume %+v calls %v", o, f.calls)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("live tree removed: %v", err)
	}
	if f.run.Cells[0].State != CellRunning {
		t.Fatal("live cell reset")
	}
}

func TestPaidCellSettlesWithoutWindows(t *testing.T) {
	// Kills using quota-window presence as proof that USD admission started.
	for _, refused := range []bool{false, true} {
		t.Run(map[bool]string{false: "measured", true: "refunded"}[refused], func(t *testing.T) {
			f := newRunFixture(t, []string{"t1"}, []string{"codex/sol/medium"})
			f.runner.Billing = paidCodex(100, 100, 0)
			f.runner.Quota = &QuotaCache{Read: func(context.Context, string) ([]Window, error) { return nil, nil }, Now: func() time.Time { return testTime }}
			f.cell(func(Cell, TaskRecord) (CellResult, error) {
				if refused {
					return CellResult{}, &delegate.NotStartedError{Err: errors.New("refused")}
				}
				return CellResult{State: CellDone, Passed: true, USD: 2}, nil
			})
			_, _, err := f.runner.runCell(context.Background(), 0)
			if !refused && err != nil {
				t.Fatal(err)
			}
			want := 2.0
			if refused {
				want = 0
			}
			if got := f.saved(t).Spend.PaidUSD["codex"]; got != want {
				t.Fatalf("spend %v want %v", got, want)
			}
		})
	}
}

func TestHiddenRemovalCannotFollowParentLink(t *testing.T) {
	// Kills RemoveAll following a candidate-created parent symlink outside its tree.
	tree, outside, runDir := project(t), t.TempDir(), t.TempDir()
	marker := filepath.Join(outside, "hidden_test.go")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(tree, "tests")); err != nil {
		t.Skip(err)
	}
	write(t, runDir, filepath.Join(TasksDir, "t1", "hidden.patch"), "invalid patch\n")
	_ = applyHidden(context.Background(), runDir, tree, TaskRecord{ManifestTask: ManifestTask{ID: "t1", Hidden: []string{"tests/hidden_test.go"}}})
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("outside file removed: %v", err)
	}
}

func TestHiddenValidationDisablesGoCache(t *testing.T) {
	// Kills repeated cached Go successes masquerading as independent validation runs.
	root, c := sumProject(t)
	runner := func(_ context.Context, dir string, commands []string) []journal.Check {
		if !strings.Contains(commands[0], "-count=1") {
			t.Errorf("cache enabled: %s", commands[0])
		}
		exit := 0
		if !sumDefined(t, dir) {
			exit = 1
		}
		return []journal.Check{{Command: commands[0], Exit: exit}}
	}
	if _, _, err := Validate(context.Background(), root, c, goChecks, time.Second, runner); err != nil {
		t.Fatal(err)
	}
}
