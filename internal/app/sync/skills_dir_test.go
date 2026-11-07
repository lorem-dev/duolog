package agentsync

import (
	"slices"
	"testing"
)

// codexFolder is the real formats with Codex's project skills folder set to
// dir, as agents.codex.skills_dir does.
func codexFolder(dir string) Formats {
	return realFormats().Configured(map[string]string{"codex": dir})
}

func runIn(t *testing.T, root string, formats Formats, o Options) Report {
	t.Helper()
	o.Resolver = testModels
	rep, err := Run(root, formats, syncCfg(t), o)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// TestSkillsDirDefaultFolder: with no key, skills go to .agents/skills.
func TestSkillsDirDefaultFolder(t *testing.T) {
	root := skillProject(t)
	runIn(t, root, realFormats().Configured(nil), Options{From: "claude"})
	if !exists(root, ".agents/skills/probe/SKILL.md") || exists(root, ".codex/skills") {
		t.Error("default folder not used")
	}
}

// TestSkillsDirConfiguredFolder: with the key, skills go to the folder it
// names, and the dry run says so.
func TestSkillsDirConfiguredFolder(t *testing.T) {
	root := skillProject(t)
	formats := codexFolder(".codex/skills")
	dry := runIn(t, root, formats, Options{From: "claude", SkillsOnly: true, DryRun: true})
	if !slices.Contains(dry.Written, ".codex/skills/probe/SKILL.md") {
		t.Errorf("dry run wrote %v, want .codex/skills/probe/SKILL.md", dry.Written)
	}
	runIn(t, root, formats, Options{From: "claude"})
	if !exists(root, ".codex/skills/probe/SKILL.md") || exists(root, ".agents") {
		t.Error("configured folder not used, or .agents created")
	}
	if _, ok := lockOf(t, root).Files[".codex/skills/probe/SKILL.md"]; !ok {
		t.Error("lock does not own the file in the new folder")
	}
}

// TestSkillsDirChangeMovesOwnedFiles: after the key changes, the next run
// writes the new folder and removes the files it owned from the old one. A
// foreign file keeps the old folder alive; an empty old folder goes.
func TestSkillsDirChangeMovesOwnedFiles(t *testing.T) {
	root := skillProject(t)
	runIn(t, root, realFormats(), Options{From: "claude"})
	if !exists(root, ".agents/skills/probe/SKILL.md") {
		t.Fatal("first run did not use the default folder")
	}
	rep := runIn(t, root, codexFolder(".codex/skills"), Options{})
	if !slices.Contains(rep.Written, ".codex/skills/probe/SKILL.md") || !slices.Contains(rep.Removed, ".agents/skills/probe/SKILL.md") {
		t.Errorf("report = %+v", rep)
	}
	if exists(root, ".agents") {
		t.Error("empty old folder left behind")
	}
	lock := lockOf(t, root)
	for key := range lock.Files {
		if key[:len(".agents")] == ".agents" {
			t.Errorf("lock still names %s", key)
		}
	}
	if _, ok := lock.Files[".codex/skills/probe/scripts/a.sh"]; !ok {
		t.Error("ownership did not move to the new folder")
	}
}

// TestSkillsDirChangeLeavesForeignFiles: a file duolog does not own stays in
// the old folder, and so does an owned file that was edited by hand.
func TestSkillsDirChangeLeavesForeignFiles(t *testing.T) {
	root := skillProject(t)
	runIn(t, root, realFormats(), Options{From: "claude"})
	put(t, root, ".agents/skills/mine/SKILL.md", "mine", 0o644)
	put(t, root, ".agents/skills/probe/references/b.md", "edited", 0o644)
	rep := runIn(t, root, codexFolder(".codex/skills"), Options{})
	if got := read(t, root, ".agents/skills/mine/SKILL.md"); got != "mine" {
		t.Errorf("foreign file changed: %q", got)
	}
	if got := read(t, root, ".agents/skills/probe/references/b.md"); got != "edited" {
		t.Errorf("edited file removed or changed: %q", got)
	}
	if exists(root, ".agents/skills/probe/SKILL.md") {
		t.Error("owned file left in the old folder")
	}
	if len(rep.Conflicts) == 0 {
		t.Error("no note about the edited file")
	}
}

// TestSkillsDirIsTheSourceFolder: with Codex as the source, skills are read
// from the configured folder.
func TestSkillsDirIsTheSourceFolder(t *testing.T) {
	root := newProject(t)
	put(t, root, ".codex/skills/probe/SKILL.md", skillMD, 0o644)
	runIn(t, root, codexFolder(".codex/skills"), Options{From: "codex", SkillsOnly: true})
	if !exists(root, ".claude/skills/probe/SKILL.md") {
		t.Error("skill in the configured folder was not synced")
	}
}
