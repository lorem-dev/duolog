package hook

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	agentsync "github.com/lorem-dev/duolog/internal/app/sync"
)

const skillsDirConfig = "version: 1\nagents:\n  codex:\n    skills_dir: .codex/skills\n"

// skillsDirProject is a synced project whose config puts Codex's skills in
// .codex/skills.
func skillsDirProject(t *testing.T, agent string) Env {
	t.Helper()
	e, _, _ := setup(t, agent)
	for rel, data := range map[string]string{
		".duolog/config.yaml":                  skillsDirConfig,
		".claude/skills/probe/SKILL.md":        "---\nname: probe\ndescription: d\n---\nv1\n",
		".claude/skills/probe/references/b.md": "b1\n",
	} {
		path := filepath.Join(e.Root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, path, data)
	}
	formats := e.Sync.Configured(map[string]string{"codex": ".codex/skills"})
	if _, err := agentsync.Run(e.Root, formats, defaults(t).Sync, agentsync.Options{From: "claude"}); err != nil {
		t.Fatal(err)
	}
	return e
}

func fileIs(t *testing.T, path, want string) {
	t.Helper()
	got, _ := os.ReadFile(path)
	if string(got) != want {
		t.Errorf("%s holds %q, want %q", path, got, want)
	}
}

// The hooks sync into the folder the config names. Without Configured in
// syncConfig they would write .agents/skills.
func TestPostToolSyncsIntoConfiguredSkillsDir(t *testing.T) {
	e := skillsDirProject(t, "claude")
	path := filepath.Join(e.Root, ".claude/skills/probe/references/b.md")
	write(t, path, "b2\n")
	e = change(t, e, "tool_input", map[string]any{"file_path": path})
	silent(t, PostTool(context.Background(), e))
	fileIs(t, filepath.Join(e.Root, ".codex/skills/probe/references/b.md"), "b2\n")
	if _, err := os.Stat(filepath.Join(e.Root, ".agents")); !os.IsNotExist(err) {
		t.Fatal(".agents was created")
	}
}

func TestSessionStartSyncsIntoConfiguredSkillsDir(t *testing.T) {
	e := skillsDirProject(t, "claude")
	write(t, filepath.Join(e.Root, ".claude/skills/probe/references/b.md"), "b3\n")
	content(t, SessionStart(context.Background(), e), "SessionStart")
	fileIs(t, filepath.Join(e.Root, ".codex/skills/probe/references/b.md"), "b3\n")
	if _, err := os.Stat(filepath.Join(e.Root, ".agents")); !os.IsNotExist(err) {
		t.Fatal(".agents was created")
	}
}

// In a linked worktree the work root has the files, so its config decides,
// not the config of the root that holds the state.
func TestPostToolReadsSkillsDirFromWorkRoot(t *testing.T) {
	e := skillsDirProject(t, "claude")
	main, work := e.Root, t.TempDir()
	for _, rel := range []string{".claude/skills/probe/SKILL.md", ".claude/skills/probe/references/b.md", ".duolog/config.yaml", ".duolog/sync.lock.json"} {
		data, err := os.ReadFile(filepath.Join(main, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(work, filepath.FromSlash(rel))), 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(work, filepath.FromSlash(rel)), string(data))
	}
	// The main checkout names another folder than the worktree.
	write(t, filepath.Join(main, ".duolog", "config.yaml"), "version: 1\nagents:\n  codex:\n    skills_dir: elsewhere/skills\n")
	e.WorkRoot = work
	path := filepath.Join(work, ".claude/skills/probe/references/b.md")
	write(t, path, "b4\n")
	e = change(t, e, "tool_input", map[string]any{"file_path": path})
	silent(t, PostTool(context.Background(), e))
	fileIs(t, filepath.Join(work, ".codex/skills/probe/references/b.md"), "b4\n")
	if _, err := os.Stat(filepath.Join(main, "elsewhere")); !os.IsNotExist(err) {
		t.Fatal("the config of the state root was used")
	}
}
