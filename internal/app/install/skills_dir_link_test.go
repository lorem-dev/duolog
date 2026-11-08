package install

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The configured folder is the default folder seen through a link: install
// does not delete through the link, and does not count the folder twice.
func TestSkillsDirLinkToDefaultFolderKeepsSkills(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("links need a privilege on Windows")
	}
	home, root := setup(t)
	copyIn(t, root, ".agents/skills", "duolog")
	if err := os.MkdirAll(filepath.Join(root, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", ".agents", "skills"), filepath.Join(root, ".codex", "skills")); err != nil {
		t.Skip(err)
	}
	o := Options{Scope: "project", Agent: "codex", Home: home, Root: root}
	rep, err := codexIn(".codex/skills").Run(o)
	if err != nil {
		t.Fatal(err)
	}
	if !isThere(root, ".codex/skills/duolog/SKILL.md") || !isThere(root, ".agents/skills/duolog/SKILL.md") {
		t.Error("install deleted through the link")
	}
	if len(rep.Notes) == 0 {
		t.Error("no note about the link")
	}
}

// On a file system that ignores case, .Agents/skills is the default folder:
// its copy goes once, with one note.
func TestSkillsDirCaseOnlySpellingKeepsSkills(t *testing.T) {
	home, root := setup(t)
	copyIn(t, root, ".agents/skills", "duolog")
	if _, err := os.Stat(filepath.Join(root, ".AGENTS")); err != nil {
		t.Skip("this file system respects case")
	}
	o := Options{Scope: "project", Agent: "codex", Home: home, Root: root}
	rep, err := codexIn(".Agents/skills").Run(o)
	if err != nil {
		t.Fatal(err)
	}
	removed := 0
	for _, n := range rep.Notes {
		if strings.HasPrefix(n, "removed ") {
			removed++
		}
	}
	if isThere(root, ".agents/skills/duolog") || removed != 1 {
		t.Errorf("copy there %v, %d removal notes", isThere(root, ".agents/skills/duolog"), removed)
	}
}

// An older duolog shipped other files in its skills. Copies whose every file
// some duolog wrote go, with those files; every other skill stays.
func TestSkillsDirInstallRemovesUpgradeLeftovers(t *testing.T) {
	home, root := setup(t)
	old := "an older duolog skill\n"
	reference := "gone in the new release\n"
	withKnown(t, old)
	withKnown(t, reference)
	write(t, filepath.Join(root, ".agents/skills/duolog/SKILL.md"), old)
	write(t, filepath.Join(root, ".agents/skills/duolog/old-reference.md"), reference)
	copyIn(t, root, ".agents/skills", "duolog-peers")
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
	if isThere(root, ".codex/skills") {
		t.Error("skills written into the configured folder")
	}
}
