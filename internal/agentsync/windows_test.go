package agentsync

import "testing"

func TestWindowsSyncPaths(t *testing.T) {
	for _, tc := range []struct{ root, path, want string }{
		{`C:\Project`, `c:\PROJECT\.claude\agents\Review.md`, ".claude/agents/review.md"},
		{`C:\Project`, `.claude\agents\review.md`, ".claude/agents/review.md"},
		{`C:\Project`, `D:\other\file`, ""},
		{`C:\Project`, `..\outside`, ""},
	} {
		if got := relative(tc.root, tc.path); got != tc.want {
			t.Errorf("%+v: %q", tc, got)
		}
	}
	root := newProject(t)
	cfg := syncCfg(t)
	put(t, root, ".claude/agents/a.md", agentMD("a", "opus"), 0644)
	rep, err := Run(root, cfg, Options{From: "claude", Path: `.claude\agents\a.md`})
	if err != nil || len(rep.Written) != 1 || len(rep.Conflicts) != 0 {
		t.Fatal(rep, err)
	}
}

func TestWindowsPathSelectsMixedCaseSkill(t *testing.T) {
	root := newProject(t)
	cfg := syncCfg(t)
	put(t, root, ".claude/skills/MySkill/SKILL.md", "---\nname: MySkill\ndescription: A skill.\n---\nbody\n", 0644)
	rep, err := Run(root, cfg, Options{From: "claude", Path: `.CLAUDE\SKILLS\MYSKILL\SKILL.MD`})
	if err != nil || len(rep.Written) != 1 || len(rep.Conflicts) != 0 {
		t.Fatal(rep, err)
	}
}
