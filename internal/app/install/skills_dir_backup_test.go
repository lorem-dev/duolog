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

// A backup of the same file may exist already, from an earlier install. A hand
// edit must not be lost behind it: it goes to a new name.
func TestSkillsDirLeftoverKeepsEarlierBackup(t *testing.T) {
	home, root := setup(t)
	write(t, filepath.Join(root, ".agents/skills/duolog/SKILL.md"), "a hand edit")
	write(t, filepath.Join(root, ".duolog/backup/.agents/skills/duolog/SKILL.md"), "an earlier backup")
	o := Options{Scope: "project", Agent: "codex", Home: home, Root: root}
	if _, err := codexIn(".codex/skills").Run(o); err != nil {
		t.Fatal(err)
	}
	if isThere(root, ".agents/skills/duolog/SKILL.md") {
		t.Error("the leftover was not removed")
	}
	dir := filepath.Join(root, ".duolog/backup/.agents/skills/duolog")
	if got := read(t, filepath.Join(dir, "SKILL.md")); got != "an earlier backup" {
		t.Errorf("the earlier backup changed to %q", got)
	}
	if got := read(t, filepath.Join(dir, "SKILL.md.1")); got != "a hand edit" {
		t.Errorf("the hand edit is saved as %q", got)
	}
}

// A backup with the same content is enough: no second copy.
func TestSkillsDirLeftoverSameBackupWritesNoCopy(t *testing.T) {
	home, root := setup(t)
	write(t, filepath.Join(root, ".agents/skills/duolog/SKILL.md"), "same")
	write(t, filepath.Join(root, ".duolog/backup/.agents/skills/duolog/SKILL.md"), "same")
	o := Options{Scope: "project", Agent: "codex", Home: home, Root: root}
	if _, err := codexIn(".codex/skills").Run(o); err != nil {
		t.Fatal(err)
	}
	if isThere(root, ".duolog/backup/.agents/skills/duolog/SKILL.md.1") {
		t.Error("a copy of an identical backup was written")
	}
}

// With no backup, the file stays and the install fails.
func TestSkillsDirLeftoverStaysWhenBackupFails(t *testing.T) {
	home, root := setup(t)
	write(t, filepath.Join(root, ".agents/skills/duolog/SKILL.md"), "a hand edit")
	// A folder in the place of the backup file cannot be read or replaced.
	if err := os.MkdirAll(filepath.Join(root, ".duolog/backup/.agents/skills/duolog/SKILL.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	o := Options{Scope: "project", Agent: "codex", Home: home, Root: root}
	_, err := codexIn(".codex/skills").Run(o)
	if err == nil {
		t.Error("install did not fail")
	}
	if got := read(t, filepath.Join(root, ".agents/skills/duolog/SKILL.md")); got != "a hand edit" {
		t.Errorf("the file was removed or changed: %q", got)
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
