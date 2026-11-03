package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSkillTestsPreference(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("APPDATA", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	// A separate root keeps this test away from repository configuration.
	project := t.TempDir()
	path := filepath.Join(project, ".duolog", "config.local.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(project)
	if err != nil || cfg.SkillTests != "ask" {
		t.Fatalf("%+v %v", cfg, err)
	}
	for _, value := range []string{"ask", "always", "never", "typo"} {
		if err := os.WriteFile(path, []byte("skill_tests: "+value+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(project)
		if value == "typo" {
			if err == nil {
				t.Fatal("accepted invalid preference")
			}
			continue
		}
		if err != nil || cfg.SkillTests != value {
			t.Fatalf("%+v %v", cfg, err)
		}
	}
}
