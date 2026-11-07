//go:build windows

package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A junction is not a symbolic link to Go, but install must treat it as one.
func TestSkillsDirLeftoverSkipsJunction(t *testing.T) {
	home, root := setup(t)
	shared := t.TempDir()
	write(t, filepath.Join(shared, "duolog", "SKILL.md"), "shared duolog")
	if err := os.MkdirAll(filepath.Join(root, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, ".agents", "skills")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, shared).CombinedOutput(); err != nil {
		t.Skipf("no junction: %v %s", err, out)
	}
	o := Options{Scope: "project", Agent: "codex", Home: home, Root: root}
	if _, err := codexIn(".codex/skills").Run(o); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(shared, "duolog", "SKILL.md")); got != "shared duolog" {
		t.Errorf("the shared skill changed to %q", got)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Errorf("the junction is gone: %v", err)
	}
}
