package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A symbolic link, and on Windows a junction (a reparse point that Go reports
// as irregular), count as links. A folder and a file do not.
func TestLinkModePredicate(t *testing.T) {
	for mode, want := range map[os.FileMode]bool{
		os.ModeSymlink:           true,
		os.ModeIrregular:         true,
		os.ModeDir | os.ModePerm: false,
		0o644:                    false,
		os.ModeDir:               false,
	} {
		if got := isLinkMode(mode); got != want {
			t.Errorf("isLinkMode(%v) = %t, want %t", mode, got, want)
		}
	}
}

// The note about a link names only the skills that exist: the person has no use
// for a note about a skill that is not there.
func TestSkillsDirLinkNoteOnlyForExistingSkills(t *testing.T) {
	skipWithoutLinks(t)
	home, root := setup(t)
	shared := t.TempDir()
	write(t, filepath.Join(shared, "duolog/SKILL.md"), "shared duolog")
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
	var left []string
	for _, n := range rep.Notes {
		if strings.HasPrefix(n, "left ") {
			left = append(left, n)
		}
	}
	if len(left) != 1 || !strings.Contains(left[0], filepath.Join(".agents", "skills", "duolog")+": ") {
		t.Errorf("notes %q, want one note, for the duolog skill", left)
	}
}
