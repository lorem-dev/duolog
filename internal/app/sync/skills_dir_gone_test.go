package agentsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// codexTwoSkills is a project whose Codex skills folder holds two skills,
// synced to Claude.
func codexTwoSkills(t *testing.T) string {
	t.Helper()
	root := newProject(t)
	put(t, root, ".agents/skills/probe/SKILL.md", skillMD, 0o644)
	put(t, root, ".agents/skills/other/SKILL.md", strings.Replace(skillMD, "probe", "other", 1), 0o644)
	runIn(t, root, realFormats(), Options{From: "codex"})
	if !exists(root, ".claude/skills/probe/SKILL.md") || !exists(root, ".claude/skills/other/SKILL.md") {
		t.Fatal("first run did not generate both copies")
	}
	return root
}

func move(t *testing.T, root, from, to string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, to)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, from), filepath.Join(root, to)); err != nil {
		t.Fatal(err)
	}
}

// The person changes the key and moves the skills, but one is dropped on the
// way. Its source is gone, so the generated copy goes as for any deleted
// source, and the run does not ask for a move again.
func TestSkillsDirDroppedSkillIsRemovedNotRefused(t *testing.T) {
	root := codexTwoSkills(t)
	formats := codexFolder(".codex/skills")
	move(t, root, ".agents/skills/probe", ".codex/skills/probe")
	if err := os.RemoveAll(filepath.Join(root, ".agents")); err != nil {
		t.Fatal(err)
	}
	rep := runIn(t, root, formats, Options{})
	if len(rep.Conflicts) != 0 {
		t.Errorf("conflicts %q", rep.Conflicts)
	}
	if exists(root, ".claude/skills/other/SKILL.md") || !exists(root, ".claude/skills/probe/SKILL.md") {
		t.Errorf("report %+v: the copy of the dropped skill must go and the other must stay", rep)
	}
	// The next run has nothing left to say.
	if rep := runIn(t, root, formats, Options{}); len(rep.Conflicts)+len(rep.Removed)+len(rep.Written) != 0 {
		t.Errorf("second run %+v", rep)
	}
}

// The old folder is deleted with every skill in it: the sources are gone.
func TestSkillsDirOldFolderDeletedIsNotRefused(t *testing.T) {
	root := codexTwoSkills(t)
	if err := os.RemoveAll(filepath.Join(root, ".agents")); err != nil {
		t.Fatal(err)
	}
	rep := runIn(t, root, codexFolder(".codex/skills"), Options{})
	if len(rep.Conflicts) != 0 {
		t.Errorf("conflicts %q", rep.Conflicts)
	}
}

// A generated copy that is already missing leaves only a lock entry, and the
// run drops it instead of asking for a move.
func TestSkillsDirMissingCopyDropsLockEntry(t *testing.T) {
	root := codexTwoSkills(t)
	if err := os.Remove(filepath.Join(root, ".claude/skills/other/SKILL.md")); err != nil {
		t.Fatal(err)
	}
	rep := runIn(t, root, codexFolder(".codex/skills"), Options{})
	if _, ok := lockOf(t, root).Files[".claude/skills/other/SKILL.md"]; ok {
		t.Error("the lock still owns a file that is gone")
	}
	// The probe copy still has its source in the old folder, so it is asked for.
	if !strings.Contains(strings.Join(rep.Conflicts, "\n"), "move the skills") {
		t.Errorf("conflicts %q", rep.Conflicts)
	}
}
