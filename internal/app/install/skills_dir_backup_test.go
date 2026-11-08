package install

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func skipWithoutLinks(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("links need a privilege on Windows")
	}
}

func isLink(t *testing.T, path string) bool {
	t.Helper()
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

// A copy with a hand edit is the person's now: install leaves it as it is,
// keeps no backup of it, and says so.
func TestSkillsDirHandEditedCopyStays(t *testing.T) {
	home, root := setup(t)
	write(t, filepath.Join(root, ".agents/skills/duolog/SKILL.md"), "a hand edit")
	o := Options{Scope: "project", Agent: "codex", Home: home, Root: root}
	rep, err := codexIn(".codex/skills").Run(o)
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(root, ".agents/skills/duolog/SKILL.md")); got != "a hand edit" {
		t.Errorf("the copy changed to %q", got)
	}
	if isThere(root, ".duolog/backup/.agents") {
		t.Error("a backup was written")
	}
	if !strings.Contains(strings.Join(rep.Notes, "\n"), "left "+filepath.Join(root, ".agents/skills/duolog")+": ") {
		t.Errorf("no note in %v", rep.Notes)
	}
}

// .agents/skills is a link to a shared folder: install leaves the shared
// folder and the link alone, and says so.
func TestSkillsDirLeftoverSkipsLinkedSkillsFolder(t *testing.T) {
	skipWithoutLinks(t)
	home, root := setup(t)
	shared := t.TempDir()
	write(t, filepath.Join(shared, "duolog/SKILL.md"), "shared duolog")
	write(t, filepath.Join(shared, "mine/SKILL.md"), "mine")
	if err := os.MkdirAll(filepath.Join(root, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, filepath.Join(root, ".agents", "skills")); err != nil {
		t.Skip(err)
	}
	o := Options{Scope: "project", Agent: "codex", Home: home, Root: root}
	rep, err := codexIn(".codex/skills").Run(o)
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(shared, "duolog/SKILL.md")); got != "shared duolog" {
		t.Errorf("the shared skill changed to %q", got)
	}
	if !isLink(t, filepath.Join(root, ".agents/skills")) {
		t.Error(".agents/skills is not a link any more")
	}
	if !strings.Contains(strings.Join(rep.Notes, "\n"), "link") {
		t.Errorf("no note about the link in %v", rep.Notes)
	}
}

// .agents/skills/duolog is a link to a copy being developed: install does not
// delete through it or unlink it.
func TestSkillsDirLeftoverSkipsLinkedSkill(t *testing.T) {
	skipWithoutLinks(t)
	home, root := setup(t)
	dev := t.TempDir()
	write(t, filepath.Join(dev, "SKILL.md"), "dev copy")
	if err := os.MkdirAll(filepath.Join(root, ".agents/skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dev, filepath.Join(root, ".agents/skills/duolog")); err != nil {
		t.Skip(err)
	}
	o := Options{Scope: "project", Agent: "codex", Home: home, Root: root}
	if _, err := codexIn(".codex/skills").Run(o); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dev, "SKILL.md")); got != "dev copy" {
		t.Errorf("the dev copy changed to %q", got)
	}
	if !isLink(t, filepath.Join(root, ".agents/skills/duolog")) {
		t.Error("the link to the dev copy is gone")
	}
}
