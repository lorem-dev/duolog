package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/plugin"
)

func TestSkillCheckIsShortAndInstalled(t *testing.T) {
	for _, scope := range []string{"project", "user"} {
		t.Run(scope, func(t *testing.T) {
			root, home := t.TempDir(), t.TempDir()
			o := Options{Scope: scope, Agent: "both", Root: root, Home: home}
			if _, err := Run(o); err != nil {
				t.Fatal(err)
			}
			for _, agent := range []string{"claude", "codex"} {
				data, err := plugin.FS.ReadFile(agent + "/skills/duolog-skill-check/SKILL.md")
				if err != nil {
					t.Fatal(err)
				}
				if n := words(string(data)); n >= 300 {
					t.Fatalf("%s: %d words", agent, n)
				}
				base := root
				if scope == "user" {
					base = home
				}
				path := filepath.Join(filepath.Dir(filepath.Dir(installedSkillPath(o, base, agent))), "duolog-skill-check", "SKILL.md")
				have, err := os.ReadFile(path)
				if err != nil || string(have) != string(data) {
					t.Fatalf("%s %v", path, err)
				}
			}
			if err := Remove(o); err != nil {
				t.Fatal(err)
			}
			base := root
			if scope == "user" {
				base = home
			}
			for _, agent := range []string{"claude", "codex"} {
				path := filepath.Join(filepath.Dir(filepath.Dir(installedSkillPath(o, base, agent))), "duolog-skill-check", "SKILL.md")
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("skill remains: %s %v", path, err)
				}
			}
		})
	}
}
