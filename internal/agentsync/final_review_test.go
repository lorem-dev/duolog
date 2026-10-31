package agentsync

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/models"
	"github.com/lorem-dev/duolog/internal/testbin"
)

func TestConcurrentSyncKeepsOwnership(t *testing.T) {
	root := newProject(t)
	cfg := syncCfg(t)
	put(t, root, ".claude/agents/a.md", agentMD("a", "opus"), 0644)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rep, err := Run(root, cfg, Options{From: "claude"})
			if err != nil || len(rep.Conflicts) > 0 {
				t.Errorf("%+v %v", rep, err)
			}
		}()
	}
	wg.Wait()
	rep, err := Run(root, cfg, Options{Check: true})
	if err != nil || len(rep.Conflicts)+len(rep.Written)+len(rep.Removed) > 0 {
		t.Fatal(rep, err)
	}
}

func TestScanErrorNeverRemovesOwnedFiles(t *testing.T) {
	root := newProject(t)
	cfg := syncCfg(t)
	put(t, root, ".claude/agents/a.md", agentMD("a", "opus"), 0644)
	put(t, root, ".claude/skills/s/SKILL.md", "---\nname: s\ndescription: Skill.\n---\nDo work.\n", 0644)
	if rep, err := Run(root, cfg, Options{From: "claude"}); err != nil || len(rep.Conflicts) > 0 {
		t.Fatal(rep, err)
	}
	// ENOTDIR exercises real ReadDir failure even when tests run as root.
	if err := os.RemoveAll(filepath.Join(root, ".claude", "agents")); err != nil {
		t.Fatal(err)
	}
	put(t, root, ".claude/agents", "unreadable as a directory", 0644)
	if err := os.RemoveAll(filepath.Join(root, ".claude", "skills")); err != nil {
		t.Fatal(err)
	}
	put(t, root, ".claude/skills", "unreadable as a directory", 0644)
	rep, err := Run(root, cfg, Options{})
	if err != nil || len(rep.Conflicts) != 2 || len(rep.Removed) != 0 {
		t.Fatal(rep, err)
	}
	for _, target := range []string{".codex/agents/a.toml", ".agents/skills/s/SKILL.md"} {
		if !exists(root, target) {
			t.Fatalf("deleted %s after scan failure", target)
		}
	}
}

func TestReadSourceDirReportsAFileInTheWay(t *testing.T) {
	root := t.TempDir()
	put(t, root, "file", "not a directory", 0644)
	for _, path := range []string{"file", "file/below"} {
		if _, err := readSourceDir(filepath.Join(root, path)); err == nil || errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s: %v", path, err)
		}
	}
	if _, err := readSourceDir(filepath.Join(root, "missing", "below")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestSameBytesHandPlacedFileNeverBecomesOwned(t *testing.T) {
	root := newProject(t)
	cfg := syncCfg(t)
	put(t, root, ".claude/skills/s/SKILL.md", "---\nname: s\ndescription: Skill.\n---\nDo work.\n", 0644)
	put(t, root, ".claude/skills/s/asset.txt", "same bytes", 0644)
	put(t, root, ".agents/skills/s/asset.txt", "same bytes", 0644)
	rep, err := Run(root, cfg, Options{From: "claude"})
	if err != nil || len(rep.Conflicts) != 1 {
		t.Fatal(rep, err)
	}
	lock := lockOf(t, root)
	if _, owned := lock.Files[".agents/skills/s/asset.txt"]; owned {
		t.Fatal("adopted hand-placed file")
	}
	if err := os.Remove(filepath.Join(root, ".claude/skills/s/asset.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(root, cfg, Options{}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, root, ".agents/skills/s/asset.txt"); got != "same bytes" {
		t.Fatal(got)
	}
}

func TestHookCatalogHasDeadlineAndRecovers(t *testing.T) {
	root := newProject(t)
	cfg := syncCfg(t)
	put(t, root, ".claude/agents/a.md", agentMD("a", "opus"), 0644)
	if rep, err := Run(root, cfg, Options{From: "claude"}); err != nil || len(rep.Conflicts) > 0 {
		t.Fatal(rep, err)
	}
	put(t, root, ".claude/agents/a.md", agentMD("a", "sonnet"), 0644)
	CatalogList = func(ctx context.Context) ([]models.Entry, string, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 2100*time.Millisecond {
			t.Error("unbounded hook metadata")
		}
		panic("metadata panic")
	}
	// Command hook recovery is covered separately. The resolver's supplied
	// context is checked through the actual SessionStart -> Run path here.
	func() {
		defer func() {
			if recover() == nil {
				t.Error("catalog fixture was not used")
			}
		}()
		SessionStart(root, cfg)
	}()
	CatalogList = nil
	fake := testbin.Install(t, "codex", testbin.Scenario{SleepMS: 60000})
	_ = fake
	started := time.Now()
	line := SessionStart(root, cfg)
	if time.Since(started) > 3*time.Second {
		t.Fatal(line, time.Since(started))
	}
}

func TestCheckDoesNotCreateMutexInUnsyncedProject(t *testing.T) {
	root := newProject(t)
	put(t, root, ".duolog/config.yaml", "version: 1\n", 0644)
	if _, err := Run(root, syncCfg(t), Options{From: "claude", Check: true}); err != nil {
		t.Fatal(err)
	}
	if exists(root, ".duolog/sync.lock") || exists(root, ".duolog/sync.lock.json") {
		t.Fatal("check wrote state")
	}
}

// A linked worktree of a repo that commits its generated files starts with the
// lock it checked out, so those files are not mistaken for hand-written ones.
func TestLinkedWorktreeKeepsCommittedGeneratedFiles(t *testing.T) {
	main := newProject(t)
	cfg := syncCfg(t)
	put(t, main, ".git/HEAD", "ref: refs/heads/main\n", 0644)
	put(t, main, ".git/config", "[core]\nrepositoryformatversion = 0\nbare = false\n", 0644)
	for _, dir := range []string{"objects", "refs/heads"} {
		if err := os.MkdirAll(filepath.Join(main, ".git", filepath.FromSlash(dir)), 0755); err != nil {
			t.Fatal(err)
		}
	}
	put(t, main, ".claude/agents/a.md", agentMD("a", "opus"), 0644)
	if rep, err := Run(main, cfg, Options{From: "claude"}); err != nil || len(rep.Conflicts) > 0 {
		t.Fatal(rep, err)
	}
	work := t.TempDir()
	meta := filepath.Join(main, ".git", "worktrees", "feature")
	put(t, meta, "HEAD", "ref: refs/heads/feature\n", 0644)
	put(t, meta, "commondir", "../..\n", 0644)
	put(t, meta, "gitdir", filepath.Join(work, ".git")+"\n", 0644)
	put(t, work, ".git", "gitdir: "+meta+"\n", 0644)
	for _, rel := range []string{".claude/agents/a.md", ".codex/agents/a.toml", ".duolog/sync.lock.json"} {
		data, err := os.ReadFile(filepath.Join(main, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		put(t, work, rel, string(data), 0644)
	}
	if rep, err := Run(work, cfg, Options{Check: true}); err != nil || len(rep.Conflicts) > 0 {
		t.Fatal(rep, err)
	}
	put(t, work, ".claude/agents/a.md", agentMD("a", "sonnet"), 0644)
	rep, err := Run(work, cfg, Options{})
	if err != nil || len(rep.Conflicts) > 0 || len(rep.Written) != 1 {
		t.Fatal(rep, err)
	}
	if _, err := os.Stat(filepath.Join(work, ".duolog", "sync.lock.json")); err != nil {
		t.Fatal(err)
	}
}
