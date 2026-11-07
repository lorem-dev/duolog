package install

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The configured folder is the default folder seen through a link: install
// must not take its own files out of the folder it just wrote.
func TestSkillsDirLinkToDefaultFolderKeepsSkills(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("links need a privilege on Windows")
	}
	home, root := setup(t)
	if err := os.MkdirAll(filepath.Join(root, ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", ".agents", "skills"), filepath.Join(root, ".codex", "skills")); err != nil {
		t.Skip(err)
	}
	o := Options{Scope: "project", Agent: "codex", Home: home, Root: root}
	if _, err := codexIn(".codex/skills").Run(o); err != nil {
		t.Fatal(err)
	}
	if !isThere(root, ".codex/skills/duolog/SKILL.md") || !isThere(root, ".agents/skills/duolog/SKILL.md") {
		t.Error("install removed the skills it wrote")
	}
}

// On a file system that ignores case, .Agents/skills is the default folder.
func TestSkillsDirCaseOnlySpellingKeepsSkills(t *testing.T) {
	home, root := setup(t)
	o := Options{Scope: "project", Agent: "codex", Home: home, Root: root}
	if _, err := codexIn("").Run(o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".AGENTS")); err != nil {
		t.Skip("this file system respects case")
	}
	if _, err := codexIn(".Agents/skills").Run(o); err != nil {
		t.Fatal(err)
	}
	if !isThere(root, ".agents/skills/duolog/SKILL.md") {
		t.Error("install removed the skills it wrote")
	}
}

// An older duolog shipped other files in its skills. They sit in the old
// folder, and Codex would load two copies, so install backs up and removes
// duolog's skill folders there and leaves every other skill alone.
func TestSkillsDirInstallRemovesUpgradeLeftovers(t *testing.T) {
	home, root := setup(t)
	write(t, filepath.Join(root, ".agents/skills/duolog/SKILL.md"), "an older duolog skill")
	write(t, filepath.Join(root, ".agents/skills/duolog/old-reference.md"), "gone in the new release")
	write(t, filepath.Join(root, ".agents/skills/duolog-peers/SKILL.md"), "older peers")
	write(t, filepath.Join(root, ".agents/skills/duologist/SKILL.md"), "not ours, by name")
	write(t, filepath.Join(root, ".agents/skills/mine/SKILL.md"), "mine")
	o := Options{Scope: "project", Agent: "codex", Home: home, Root: root}
	if _, err := codexIn(".codex/skills").Run(o); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".agents/skills/duolog", ".agents/skills/duolog-peers"} {
		if isThere(root, rel) {
			t.Errorf("%s was left behind", rel)
		}
	}
	for rel, want := range map[string]string{".agents/skills/mine/SKILL.md": "mine", ".agents/skills/duologist/SKILL.md": "not ours, by name"} {
		if got := read(t, filepath.Join(root, rel)); got != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	if got := read(t, filepath.Join(root, ".duolog/backup/.agents/skills/duolog/old-reference.md")); got != "gone in the new release" {
		t.Errorf("backup of the old file = %q", got)
	}
	if got := read(t, filepath.Join(root, ".duolog/backup/.agents/skills/duolog/SKILL.md")); got != "an older duolog skill" {
		t.Errorf("backup of SKILL.md = %q", got)
	}
	if !isThere(root, ".codex/skills/duolog/SKILL.md") {
		t.Error("skill not in the configured folder")
	}
}
