package delegate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/agentsync"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/hook"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/runner"
)

func TestLinkedWorktreeUsesOwnTaskCwdGateAndSync(t *testing.T) {
	h := newHarness(t)
	h.gitRepo()
	main := h.root
	work := t.TempDir()
	meta := filepath.Join(main, ".git", "worktrees", "feature")
	put(t, filepath.Join(meta, "HEAD"), "ref: refs/heads/feature\n")
	put(t, filepath.Join(meta, "commondir"), "../..\n")
	put(t, filepath.Join(meta, "gitdir"), filepath.Join(work, ".git")+"\n")
	put(t, filepath.Join(work, ".git"), "gitdir: "+meta+"\n")
	put(t, filepath.Join(work, "a.go"), "WORKTREE_CONTENT\n")
	put(t, filepath.Join(main, "a.go"), "MAIN_CONTENT_MUST_NOT_REACH_WORKER\n")
	taskText := "## Goal\nWORKTREE_TASK\n## Read\n- a.go\n## Write\n- a.go\n## Limits\nStay focused.\n## Check\nInspect.\n"
	put(t, filepath.Join(work, "task.md"), strings.ReplaceAll(taskText, "WORKTREE_TASK", "WRONG_ROOT_TASK"))
	put(t, filepath.Join(work, "nested", "task.md"), taskText)
	t.Chdir(filepath.Join(work, "nested"))
	root := project.CurrentWorkRoot(main)
	info, _ := os.Stat(root)
	want, _ := os.Stat(work)
	if info == nil || want == nil || !os.SameFile(info, want) {
		t.Fatalf("work root=%s", root)
	}
	oldRoot := h.root
	h.root = work
	h.trust(true)
	h.root = oldRoot
	stateRoot := project.StateRoot(root)
	info, _ = os.Stat(stateRoot)
	want, _ = os.Stat(main)
	if info == nil || want == nil || !os.SameFile(info, want) {
		t.Fatalf("state root=%s", stateRoot)
	}
	// State stays in main; generated files and the worker use the linked tree.
	if _, err := Run(context.Background(), h.cfg, h.s, "implement", "task.md", ""); err != nil {
		t.Fatal(err)
	}
	h.requireCalls(1)
	spec := h.calls[0]
	info, _ = os.Stat(spec.Cwd)
	want, _ = os.Stat(work)
	if info == nil || !os.SameFile(info, want) {
		t.Fatal(spec.Cwd)
	}
	stdin := string(spec.Stdin)
	if !strings.Contains(stdin, "WORKTREE_TASK") || !strings.Contains(stdin, "WORKTREE_CONTENT") || strings.Contains(stdin, "MAIN_CONTENT") || strings.Contains(stdin, "WRONG_ROOT_TASK") {
		t.Fatal(stdin)
	}
	if !strings.HasPrefix(spec.TaskDir, h.s.Dir) {
		t.Fatal("task state left shared project root")
	}
	path := gate.RelativeTarget(root, filepath.Join(work, "nested"), "../a.go")
	if path != "a.go" {
		t.Fatalf("gate used main checkout: %s", path)
	}
	put(t, filepath.Join(work, ".claude/skills/s/SKILL.md"), "---\nname: s\ndescription: Skill.\n---\nWORKTREE_SKILL\n")
	rep, err := agentsync.Run(work, h.cfg.Sync, agentsync.Options{From: "claude", SkillsOnly: true})
	if err != nil || len(rep.Conflicts) > 0 {
		t.Fatal(rep, err)
	}
	put(t, filepath.Join(work, ".claude/skills/s/SKILL.md"), "---\nname: s\ndescription: Skill.\n---\nWORKTREE_SKILL_EDIT\n")
	input := []byte(`{"session_id":"lead-123456","tool_input":{"file_path":".claude/skills/s/SKILL.md"}}`)
	hook.PostTool(context.Background(), hook.Env{Root: main, WorkRoot: work, Agent: "codex", Input: input, Environ: []string{}})
	got := read(t, filepath.Join(work, ".agents/skills/s/SKILL.md"))
	if !strings.Contains(got, "WORKTREE_SKILL_EDIT") {
		t.Fatal(got)
	}
	if _, err := os.Stat(filepath.Join(main, ".agents/skills/s/SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("sync wrote main checkout", err)
	}
	if _, err := os.Stat(filepath.Join(work, ".duolog")); !os.IsNotExist(err) {
		t.Fatal("state created in linked tree", err)
	}
}

func TestDelegateRetainsKnownEffortAndBoundsOutput(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Full, "low")
	l, _, _ := h.reg.Lead("lead-123456")
	l.TranscriptPath = filepath.Join(h.root, "empty.jsonl")
	put(t, l.TranscriptPath, "{}\n")
	if err := h.reg.RegisterLead(l); err != nil {
		t.Fatal(err)
	}
	h.results = []runner.Result{{Done: true, Summary: strings.Repeat("a", 6000), Files: []string{}, Needs: []string{}}}
	out, err := h.run("implement")
	if err != nil || len(out.Line) > 512 || !strings.Contains(out.Line, "answer.json") {
		t.Fatal(len(out.Line), out.Line, err)
	}
	if h.calls[0].Route.Effort != "low" {
		t.Fatal("known effort lost", h.calls[0].Route)
	}
}

func TestInlineRespectsLeadReadDeny(t *testing.T) {
	h := newHarness(t)
	put(t, filepath.Join(h.root, ".claude/settings.json"), `{"permissions":{"deny":["Read(private.txt)"]}}`)
	h.lead("claude", mode.Full, "high")
	h.task("- private.txt")
	put(t, filepath.Join(h.root, "private.txt"), "DO_NOT_SEND_PRIVATE_CONTENT")
	if _, err := h.run("implement"); err != nil {
		t.Fatal(err)
	}
	stdin := string(h.calls[0].Stdin)
	if strings.Contains(stdin, "DO_NOT_SEND_PRIVATE_CONTENT") || !strings.Contains(stdin, "Not inlined by read policy") {
		t.Fatal(stdin)
	}
}

func TestDelegateWarnsAboutMissingClaudeHooks(t *testing.T) {
	h := newHarness(t)
	out, err := h.run("review_final")
	if err != nil || !strings.Contains(out.Line, "Claude has no duolog hooks") {
		t.Fatal(out, err)
	}
}

func TestWorkerWaitMarkerExistsOnlyDuringRun(t *testing.T) {
	h := newHarness(t)
	previous := runWorker
	runWorker = func(ctx context.Context, s runner.Spec) (runner.Result, error) {
		if waiting, err := h.reg.IsWaiting("lead-123456"); err != nil || !waiting {
			t.Fatal("no waiting marker", err)
		}
		return previous(ctx, s)
	}
	if _, err := h.run("implement"); err != nil {
		t.Fatal(err)
	}
	if waiting, err := h.reg.IsWaiting("lead-123456"); err != nil || waiting {
		t.Fatal("wait marker leaked", err)
	}
}

func TestImportFailureStopsBeforeWorker(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Full, "high")
	put(t, filepath.Join(h.root, ".claude/settings.json"), `{"permissions":{"deny":["UnknownTool(bad)"]}}`)
	out, err := h.run("implement")
	if err == nil || !strings.Contains(out.Line, "lead permissions") {
		t.Fatal(out, err)
	}
	h.requireCalls(0)
}

func TestDefaultLoginVariablesReachWorker(t *testing.T) {
	h := newHarness(t)
	names := []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "CODEX_HOME", "CLAUDE_CONFIG_DIR"}
	for _, name := range names {
		t.Setenv(name, filepath.Join(h.root, "login-"+name))
	}
	// This read-only Claude run needs no Codex trust metadata.
	fake := h.fakeWorker("claude")
	if _, err := h.run("review_final"); err != nil {
		t.Fatal(err)
	}
	env := read(t, fake+".env.json")
	for _, name := range names {
		if !strings.Contains(env, name+"=") {
			t.Fatal("worker lost login variable", name)
		}
	}
}
