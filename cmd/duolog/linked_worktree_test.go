package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/delegate/delegatetest"
	"github.com/lorem-dev/duolog/internal/app/hook"
	agentsync "github.com/lorem-dev/duolog/internal/app/sync"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/policy"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/wiring"
)

// putWorktreeFile writes a test file, creating its folders.
func putWorktreeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// trustCodexIn writes the Codex hooks duolog installs into dir and approves
// them and the folder in the home's Codex config.
func trustCodexIn(t *testing.T, dir string) {
	t.Helper()
	hooks, err := hooksJSON("codex", 30)
	if err != nil {
		t.Fatal(err)
	}
	putWorktreeFile(t, filepath.Join(dir, ".codex", "hooks.json"), string(hooks))
	s := fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n[hooks.state.%q]\ntrusted_hash = \"fixture-hash\"\n", dir, filepath.Join(dir, ".codex", "hooks.json")+":PreToolUse:0:0")
	putWorktreeFile(t, filepath.Join(os.Getenv("HOME"), ".codex", "config.toml"), s)
}

// This test needs the real installed hooks and the real sync, so it lives
// here and not in internal/app/delegate.
func TestLinkedWorktreeUsesOwnTaskCwdGateAndSync(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(name, home)
	}
	for _, name := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR", "DUOLOG_DEPTH", "DUOLOG_LEAD"} {
		t.Setenv(name, "")
	}
	main := t.TempDir()
	t.Chdir(main)
	cfg, err := configfile.Load(main)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Routing.SmallTaskFiles = 0
	s, err := store.Open(main)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := session.Open(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterLead(session.Lead{ID: "lead-123456", Target: config.Target{Agent: "codex", Model: "lead-model"}, Mode: mode.Ask}); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"objects", "refs/heads"} {
		if err := os.MkdirAll(filepath.Join(main, ".git", dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	putWorktreeFile(t, filepath.Join(main, ".git", "HEAD"), "ref: refs/heads/main\n")
	putWorktreeFile(t, filepath.Join(main, ".git", "config"), "[core]\nrepositoryformatversion = 0\nbare = false\n")
	work := t.TempDir()
	meta := filepath.Join(main, ".git", "worktrees", "feature")
	putWorktreeFile(t, filepath.Join(meta, "HEAD"), "ref: refs/heads/feature\n")
	putWorktreeFile(t, filepath.Join(meta, "commondir"), "../..\n")
	putWorktreeFile(t, filepath.Join(meta, "gitdir"), filepath.Join(work, ".git")+"\n")
	putWorktreeFile(t, filepath.Join(work, ".git"), "gitdir: "+meta+"\n")
	putWorktreeFile(t, filepath.Join(work, "a.go"), "WORKTREE_CONTENT\n")
	putWorktreeFile(t, filepath.Join(main, "a.go"), "MAIN_CONTENT_MUST_NOT_REACH_WORKER\n")
	taskText := "## Goal\nWORKTREE_TASK\n## Read\n- a.go\n## Write\n- a.go\n## Limits\nStay focused.\n## Check\nInspect.\n"
	putWorktreeFile(t, filepath.Join(work, "task.md"), strings.ReplaceAll(taskText, "WORKTREE_TASK", "WRONG_ROOT_TASK"))
	putWorktreeFile(t, filepath.Join(work, "nested", "task.md"), taskText)
	t.Chdir(filepath.Join(work, "nested"))
	root := project.CurrentWorkRoot(main)
	info, _ := os.Stat(root)
	want, _ := os.Stat(work)
	if info == nil || want == nil || !os.SameFile(info, want) {
		t.Fatalf("work root=%s", root)
	}
	trustCodexIn(t, work)
	stateRoot := project.StateRoot(root)
	info, _ = os.Stat(stateRoot)
	want, _ = os.Stat(main)
	if info == nil || want == nil || !os.SameFile(info, want) {
		t.Fatalf("state root=%s", stateRoot)
	}
	// State stays in main; generated files and the worker use the linked tree.
	fake := delegatetest.New(t)
	if _, err := delegate.Run(context.Background(), fake.Deps(), delegate.Request{Config: cfg, StateDir: s.Dir, Role: "implement", TaskPath: "task.md"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("calls=%d want 1", len(fake.Calls))
	}
	spec := fake.Calls[0]
	info, _ = os.Stat(spec.Cwd)
	want, _ = os.Stat(work)
	if info == nil || !os.SameFile(info, want) {
		t.Fatal(spec.Cwd)
	}
	stdin := string(spec.Stdin)
	if !strings.Contains(stdin, "WORKTREE_TASK") || !strings.Contains(stdin, "WORKTREE_CONTENT") || strings.Contains(stdin, "MAIN_CONTENT") || strings.Contains(stdin, "WRONG_ROOT_TASK") {
		t.Fatal(stdin)
	}
	if !strings.HasPrefix(spec.TaskDir, s.Dir) {
		t.Fatal("task state left shared project root")
	}
	path := policy.RelativeTarget(root, filepath.Join(work, "nested"), "../a.go")
	if path != "a.go" {
		t.Fatalf("gate used main checkout: %s", path)
	}
	putWorktreeFile(t, filepath.Join(work, ".claude/skills/s/SKILL.md"), "---\nname: s\ndescription: Skill.\n---\nWORKTREE_SKILL\n")
	rep, err := agentsync.Run(work, testDeps(t).SyncFormats, cfg.Sync, agentsync.Options{From: "claude", SkillsOnly: true})
	if err != nil || len(rep.Conflicts) > 0 {
		t.Fatal(rep, err)
	}
	putWorktreeFile(t, filepath.Join(work, ".claude/skills/s/SKILL.md"), "---\nname: s\ndescription: Skill.\n---\nWORKTREE_SKILL_EDIT\n")
	input := []byte(`{"session_id":"lead-123456","tool_input":{"file_path":".claude/skills/s/SKILL.md"}}`)
	hook.PostTool(context.Background(), wiring.HookEnv(hook.Env{Root: main, WorkRoot: work, Agent: "codex", Input: input, Environ: []string{}, Sync: testDeps(t).SyncFormats}))
	got, err := os.ReadFile(filepath.Join(work, ".agents/skills/s/SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("WORKTREE_SKILL_EDIT")) {
		t.Fatal(string(got))
	}
	if _, err := os.Stat(filepath.Join(main, ".agents/skills/s/SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("sync wrote main checkout", err)
	}
	if _, err := os.Stat(filepath.Join(work, ".duolog")); !os.IsNotExist(err) {
		t.Fatal("state created in linked tree", err)
	}
}

// The Codex hooks that the delegate tests approve are the ones install writes.
func TestDelegateHooksFixtureMatchesInstall(t *testing.T) {
	want, err := hooksJSON("codex", 30)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join("..", "..", "internal", "app", "delegate", "testdata", "codex-hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("internal/app/delegate/testdata/codex-hooks.json differs from codex.HooksJSON(30); copy the install output over it")
	}
}
