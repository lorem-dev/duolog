package policy

import (
	"os"
	"os/exec"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/mode"
)

// A junction under a listed folder, such as the hidden My Music junction in
// Documents, leads out of it like a symlink, and rg -L follows it. Go reads it
// as irregular, not as a symlink, so a search of the folder must still ask.
func TestTaskReadsSearchNoFolderWithJunction(t *testing.T) {
	f := newReadsFixture(t)
	p := policy(t)
	lead := LeadRules{RoleSandbox: "read-only"}
	entry := f.path("junction")
	if err := os.MkdirAll(f.path("junction/sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path("junction/sub/a.md"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", f.path("junction/sub/out"), f.path("secret")).CombinedOutput(); err != nil {
		t.Skipf("mklink /J failed: %v %s", err, out)
	}
	grep := f.read(f.path("junction/sub"), entry)
	grep.Search = true
	for name, a := range map[string]Action{
		"rg -L of the folder": f.exec("rg -L x "+f.word("junction"), entry),
		"Grep of a subfolder": grep,
	} {
		if d := Evaluate(p, lead, mode.Ask, a); d.Kind == Allow {
			t.Errorf("%s: %+v, want no allow", name, d)
		}
	}
	if d := Evaluate(p, lead, mode.Ask, f.read(f.path("junction/sub/a.md"), entry)); d.Kind != Allow {
		t.Errorf("a plain file beside a junction: %+v, want allow", d)
	}
}
