package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

func TestRecordLimitedSubmoduleCapIsTransitive(t *testing.T) {
	f := submodule.New(t)
	r, err := Open(filepath.Join(f.Superproject, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other")
	submodule.Git(t, f.Submodule, "worktree", "add", "-q", "--detach", other)
	for i, root := range []string{f.Submodule, f.Linked, other, f.Superproject} {
		err := r.RecordLimited(Entry{ID: fmt.Sprint(i), Lead: "lead", Agent: "codex", Block: fmt.Sprint(i), WorkRoot: root}, 2)
		if i < 2 && err != nil {
			t.Fatal(err)
		}
		if i >= 2 && err == nil {
			t.Fatalf("cap bypassed from %s", root)
		}
	}
}

func TestRecordLimitedMissingWorkRootDoesNotShare(t *testing.T) {
	f := submodule.New(t)
	for _, root := range []string{f.SuperLinked, f.Linked} {
		t.Run(filepath.Base(root), func(t *testing.T) {
			testMissingPaneRoot(t, f.Superproject, root)
		})
	}
}

func testMissingPaneRoot(t *testing.T, home, root string) {
	t.Helper()
	r, err := Open(filepath.Join(home, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close("lead"); err != nil {
			t.Fatal(err)
		}
	})
	if err := r.RecordLimited(Entry{ID: "old", Lead: "lead", Agent: "codex", Block: "old", WorkRoot: root}, 1); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordLimited(Entry{ID: "new", Lead: "lead", Agent: "codex", Block: "new", WorkRoot: home}, 1); err != nil {
		t.Fatalf("missing work root consumed the cap: %v", err)
	}
}

// A pane whose group Git cannot work out is refused, not given a group of
// its own.
func TestRecordLimitedRefusesPaneOnGitFailure(t *testing.T) {
	f := submodule.New(t)
	r, err := Open(filepath.Join(f.Superproject, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	err = r.RecordLimited(Entry{ID: "pane", Lead: "lead", Agent: "codex", Block: "pane", WorkRoot: f.Nested}, 2)
	if err == nil || !strings.Contains(err.Error(), "git") {
		t.Fatal("pane recorded without its group", err)
	}
	if all, err := r.Entries(); err != nil || len(all) != 0 {
		t.Fatal(all, err)
	}
}

func TestPaneCapUsesStoredGroupWithoutGit(t *testing.T) {
	f := submodule.New(t)
	dir := filepath.Join(f.Superproject, ".duolog")
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	first := Entry{ID: "first", Lead: "lead", Agent: "codex", Block: "first", WorkRoot: f.Submodule}
	if err := r.RecordLimited(first, 1); err != nil {
		t.Fatal(err)
	}
	r, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	stored := entryOf(t, r, first.ID)
	if stored.CapGroup == "" {
		t.Fatal("pane group was not persisted")
	}
	t.Setenv("PATH", t.TempDir())
	second := Entry{ID: "second", Lead: "lead", Agent: "codex", Block: "second", WorkRoot: f.Linked, CapGroup: stored.CapGroup}
	if err := r.RecordLimited(second, 1); err == nil {
		t.Fatal("stored group was ignored without Git")
	}
}

// The cap belongs to the lead and agent: a pane in the submodule counts with
// one in the superproject, while another lead or agent keeps its own cap.
func TestRecordLimitedAcrossWorkProjects(t *testing.T) {
	f := submodule.New(t)
	r, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pane := func(id, lead, agent, root string) Entry {
		return Entry{ID: id, Lead: lead, Agent: agent, Block: "block:" + id, WorkRoot: root}
	}
	if err := r.RecordLimited(pane("super", "lead", "codex", f.Superproject), 1); err != nil {
		t.Fatal(err)
	}
	secondErr := r.RecordLimited(pane("sub", "lead", "codex", f.Linked), 1)
	otherLeadErr := r.RecordLimited(pane("other-lead", "lead-2", "codex", f.Linked), 1)
	otherAgentErr := r.RecordLimited(pane("other-agent", "lead", "claude", f.Linked), 1)
	nestedErr := r.RecordLimited(pane("nested", "lead", "claude", f.Nested), 1)
	// Peers are keyed by work root: another worktree of one project keeps
	// its own cap, as before.
	laneErr := r.RecordLimited(pane("lane", "lead", "codex", f.SuperLinked), 1)
	if secondErr == nil || nestedErr == nil {
		t.Fatal("work root bypassed cap", secondErr, nestedErr)
	}
	if otherLeadErr != nil || otherAgentErr != nil {
		t.Fatal("cap crossed owner", otherLeadErr, otherAgentErr)
	}
	if laneErr != nil {
		t.Fatal("a linked worktree of the project lost its own cap", laneErr)
	}

	evicting, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := evicting.RecordLimited(Entry{ID: "thread", Lead: "lead", Agent: "codex", Role: "plan", Thread: "t", WorkRoot: f.Linked, UpdatedAt: time.Now().Add(-time.Minute)}, 1); err != nil {
		t.Fatal(err)
	}
	if err := evicting.RecordLimited(pane("super", "lead", "codex", f.Superproject), 1); err != nil {
		t.Fatal(err)
	}
	oldThread := entryOf(t, evicting, "thread")
	if !oldThread.Closed {
		t.Fatal("stored thread was not evicted")
	}

	dir := t.TempDir()
	// One checkout of each work project, twice: every pair shares the cap.
	roots := []string{f.Superproject, f.Submodule, f.Nested, f.Superproject, f.Submodule, f.Nested}
	var wg sync.WaitGroup
	var mu sync.Mutex
	successfulConcurrentRecords := 0
	for i, root := range roots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reg, err := Open(dir)
			if err == nil {
				err = reg.RecordLimited(pane(fmt.Sprint("racer-", i), "lead", "codex", root), 1)
			}
			if err == nil {
				mu.Lock()
				successfulConcurrentRecords++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if successfulConcurrentRecords != 1 {
		t.Fatal(successfulConcurrentRecords)
	}
}

func entryOf(t *testing.T, r *Registry, id string) Entry {
	t.Helper()
	all, err := r.Entries()
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range all {
		if x.ID == id {
			return x
		}
	}
	t.Fatalf("no entry %s", id)
	return Entry{}
}
