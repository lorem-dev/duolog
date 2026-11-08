package install

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// copyIn writes an unchanged copy of Codex's bundled skill name into the
// project skills folder dir under root, as an earlier install did.
func copyIn(t *testing.T, root, dir, name string) {
	t.Helper()
	write(t, filepath.Join(root, filepath.FromSlash(dir), name, "SKILL.md"), bundled(t, "codex", name, "SKILL.md"))
}

// TestSkillsDirNoProjectSkills: with or without the key, Codex's skills go to
// the user folder and no project skills folder is created.
func TestSkillsDirNoProjectSkills(t *testing.T) {
	for _, dir := range []string{"", ".codex/skills"} {
		home, root := setup(t)
		o := Options{Scope: "project", Agent: "codex", Home: home, Root: root}
		if _, err := codexIn(dir).Run(o); err != nil {
			t.Fatal(err)
		}
		if !isThere(home, ".agents/skills/duolog/SKILL.md") || isThere(root, ".agents") || isThere(root, ".codex/skills") {
			t.Errorf("%q: skills not at user scope only", dir)
		}
	}
}

// TestSkillsDirInstallAndUninstall: the copies an earlier install wrote into
// the folder that the key names go, on install and on uninstall.
func TestSkillsDirInstallAndUninstall(t *testing.T) {
	home, root := setup(t)
	o := Options{Scope: "project", Agent: "codex", Home: home, Root: root}
	deps := codexIn(".codex/skills")
	copyIn(t, root, ".codex/skills", "duolog")
	if _, err := deps.Run(o); err != nil {
		t.Fatal(err)
	}
	if isThere(root, ".codex/skills") {
		t.Fatal("the old copy stays in .codex/skills")
	}
	copyIn(t, root, ".codex/skills", "duolog")
	if err := deps.Remove(o); err != nil {
		t.Fatal(err)
	}
	if isThere(root, ".codex/skills/duolog") {
		t.Error("the old copy stays in .codex/skills after uninstall")
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

// TestSkillsDirInstallDropsDefaultCopies: install with the key set also takes
// the unchanged copies out of the default folder, keeps a foreign skill
// there, and removes the default folder only when it ends up empty.
func TestSkillsDirInstallDropsDefaultCopies(t *testing.T) {
	home, root := setup(t)
	o := Options{Scope: "project", Agent: "codex", Home: home, Root: root}
	copyIn(t, root, ".agents/skills", "duolog")
	moved := codexIn(".codex/skills")
	rep, err := moved.Run(o)
	if err != nil {
		t.Fatal(err)
	}
	if isThere(root, ".agents") {
		t.Error(".agents left behind")
	}
	if !slices.ContainsFunc(rep.Notes, func(n string) bool {
		return strings.HasPrefix(n, "removed "+filepath.Join(root, ".agents/skills/duolog")+": ")
	}) {
		t.Errorf("no removal note in %v", rep.Notes)
	}

	// With a foreign skill in the old folder, the folder stays.
	write(t, filepath.Join(root, ".agents/skills/mine/SKILL.md"), "mine")
	copyIn(t, root, ".agents/skills", "duolog")
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
	copyIn(t, root, ".agents/skills", "duolog")
	copyIn(t, root, ".codex/skills", "duolog-peers")
	if err := moved.Remove(o); err != nil {
		t.Fatal(err)
	}
	if isThere(root, ".agents/skills/duolog") || isThere(root, ".codex/skills/duolog-peers") || !isThere(root, ".agents/skills/mine/SKILL.md") {
		t.Error("uninstall left own skills or removed a foreign one")
	}
}
