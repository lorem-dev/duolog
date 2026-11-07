package install

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/launch/launchtest"
)

// codexIn is testDeps with Codex's project skills folder set to dir.
func codexIn(dir string) Deps {
	installers := launchtest.Installers()
	return Deps{Agents: []Agent{
		{Name: "claude", Installer: installers["claude"]},
		{Name: "codex", Installer: launchtest.CodexInstaller(dir)},
	}}
}

func isThere(root, rel string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

// TestSkillsDirDefaultUnchanged: with no folder set, Codex's skills go to
// .agents/skills and nothing else is created.
func TestSkillsDirDefaultUnchanged(t *testing.T) {
	home, root := setup(t)
	o := Options{Scope: "project", Agent: "codex", Home: home, Root: root}
	if _, err := codexIn("").Run(o); err != nil {
		t.Fatal(err)
	}
	if !isThere(root, ".agents/skills/duolog/SKILL.md") || isThere(root, ".codex/skills") {
		t.Errorf("default folder not used")
	}
}

// TestSkillsDirInstallAndUninstall: the folder that the key names holds the
// skills, and uninstall removes them from there.
func TestSkillsDirInstallAndUninstall(t *testing.T) {
	home, root := setup(t)
	o := Options{Scope: "project", Agent: "codex", Home: home, Root: root}
	deps := codexIn(".codex/skills")
	if _, err := deps.Run(o); err != nil {
		t.Fatal(err)
	}
	if !isThere(root, ".codex/skills/duolog/SKILL.md") {
		t.Fatal("skill not in .codex/skills")
	}
	if isThere(root, ".agents") {
		t.Error(".agents was created")
	}
	if err := deps.Remove(o); err != nil {
		t.Fatal(err)
	}
	if isThere(root, ".codex/skills/duolog") {
		t.Error("skill still in .codex/skills after uninstall")
	}
}

// TestSkillsDirUserScopeKeepsHomeFolder: the key is for the project only.
func TestSkillsDirUserScopeKeepsHomeFolder(t *testing.T) {
	home, root := setup(t)
	o := Options{Scope: "user", Agent: "codex", Home: home, Root: root}
	if _, err := codexIn(".codex/skills").Run(o); err != nil {
		t.Fatal(err)
	}
	if !isThere(home, ".agents/skills/duolog/SKILL.md") || isThere(home, ".codex/skills") {
		t.Error("user scope did not keep ~/.agents/skills")
	}
}

// TestSkillsDirInstallMovesOwnSkills: install with the key set takes duolog's
// skills out of the default folder, keeps a foreign skill there, and removes
// the default folder only when it ends up empty.
func TestSkillsDirInstallMovesOwnSkills(t *testing.T) {
	home, root := setup(t)
	o := Options{Scope: "project", Agent: "codex", Home: home, Root: root}
	if _, err := codexIn("").Run(o); err != nil {
		t.Fatal(err)
	}
	moved := codexIn(".codex/skills")
	rep, err := moved.Run(o)
	if err != nil {
		t.Fatal(err)
	}
	if !isThere(root, ".codex/skills/duolog/SKILL.md") || isThere(root, ".agents") {
		t.Error("skills not moved, or .agents left behind")
	}
	if !slices.ContainsFunc(rep.Notes, func(n string) bool { return n == "removed "+filepath.Join(root, ".agents/skills/duolog/SKILL.md") }) {
		t.Errorf("no removal note in %v", rep.Notes)
	}

	// With a foreign skill in the old folder, the folder stays.
	write(t, filepath.Join(root, ".agents/skills/mine/SKILL.md"), "mine")
	if _, err := codexIn("").Run(o); err != nil {
		t.Fatal(err)
	}
	if _, err := moved.Run(o); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(root, ".agents/skills/mine/SKILL.md")); got != "mine" {
		t.Errorf("foreign skill changed: %q", got)
	}
	if isThere(root, ".agents/skills/duolog") {
		t.Error("own skill left in the old folder")
	}
	// Uninstall cleans both folders and still leaves the foreign file.
	if _, err := codexIn("").Run(o); err != nil {
		t.Fatal(err)
	}
	if err := moved.Remove(o); err != nil {
		t.Fatal(err)
	}
	if isThere(root, ".agents/skills/duolog") || isThere(root, ".codex/skills/duolog") || !isThere(root, ".agents/skills/mine/SKILL.md") {
		t.Error("uninstall left own skills or removed a foreign one")
	}
}
