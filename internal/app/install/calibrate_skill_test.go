package install

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/lorem-dev/duolog/plugin"
)

// splitSkill returns the frontmatter and the body of a SKILL.md.
func splitSkill(t *testing.T, path string) (front, body string) {
	t.Helper()
	data, err := plugin.FS.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rest, ok := strings.CutPrefix(string(data), "---\n")
	if !ok {
		t.Fatalf("%s: no frontmatter", path)
	}
	front, body, ok = strings.Cut(rest, "\n---\n")
	if !ok {
		t.Fatalf("%s: unterminated frontmatter", path)
	}
	return front, body
}

func TestCalibrateSkillIsExplicitOnly(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		front, body := splitSkill(t, agent+"/skills/duolog-calibrate/SKILL.md")
		var head struct {
			Name                   string `yaml:"name"`
			Description            string `yaml:"description"`
			DisableModelInvocation bool   `yaml:"disable-model-invocation"`
		}
		if err := yaml.Unmarshal([]byte(front), &head); err != nil {
			t.Fatal(err)
		}
		if head.Name != "duolog-calibrate" {
			t.Errorf("%s: name %q", agent, head.Name)
		}
		if head.Description != "Use only in a project that has .duolog/config.yaml, and only when the person asks for calibration by name; never suggest it. Calibrates duolog routing for this project." {
			t.Errorf("%s: description %q", agent, head.Description)
		}
		if agent == "claude" && !strings.Contains(front, "disable-model-invocation: true") {
			t.Errorf("claude: frontmatter lacks disable-model-invocation: true:\n%s", front)
		}
		if n := words(body); n >= 400 {
			t.Errorf("%s: body has %d words", agent, n)
		}
	}
	data, err := plugin.FS.ReadFile("codex/skills/duolog-calibrate/agents/openai.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Policy struct {
			AllowImplicitInvocation *bool `yaml:"allow_implicit_invocation"`
		} `yaml:"policy"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Policy.AllowImplicitInvocation == nil || *doc.Policy.AllowImplicitInvocation {
		t.Fatalf("policy.allow_implicit_invocation must be false:\n%s", data)
	}
}

func TestCalibrateSkillInstalledAndRemoved(t *testing.T) {
	for _, scope := range []string{"project", "user"} {
		t.Run(scope, func(t *testing.T) {
			home, root := setup(t)
			o := Options{Scope: scope, Agent: "both", Root: root, Home: home}
			if _, err := Run(o); err != nil {
				t.Fatal(err)
			}
			base := root
			if scope == "user" {
				base = home
			}
			dirs := map[string]string{}
			for _, agent := range []string{"claude", "codex"} {
				dir := filepath.Join(filepath.Dir(filepath.Dir(installedSkillPath(o, base, agent))), "duolog-calibrate")
				dirs[agent] = dir
				for _, rel := range []string{"SKILL.md", "agents/openai.yaml"} {
					if agent == "claude" && rel != "SKILL.md" {
						continue
					}
					want, err := plugin.FS.ReadFile(agent + "/skills/duolog-calibrate/" + rel)
					if err != nil {
						t.Fatal(err)
					}
					if have := read(t, filepath.Join(dir, filepath.FromSlash(rel))); have != string(want) {
						t.Fatalf("%s %s differs", agent, rel)
					}
				}
			}
			if err := Remove(o); err != nil {
				t.Fatal(err)
			}
			for agent, dir := range dirs {
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Fatalf("%s: %s remains: %v", agent, dir, err)
				}
			}
		})
	}
}

func TestNothingElseSuggestsCalibration(t *testing.T) {
	err := fs.WalkDir(plugin.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.Contains(path, "/skills/duolog-calibrate/") {
			return nil
		}
		data, err := plugin.FS.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(strings.ToLower(string(data)), "calibrat") {
			t.Errorf("%s mentions calibration", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "codex"} {
		hooks, err := HooksJSON(agent, 150)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToLower(string(hooks)), "calibrat") {
			t.Errorf("%s hooks mention calibration", agent)
		}
	}
}
