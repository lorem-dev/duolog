package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func isolatedRoot(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("APPDATA", home)
	return t.TempDir()
}

func writeConfig(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLayersLaterWins(t *testing.T) {
	root := isolatedRoot(t)
	userDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, filepath.Join(userDir, "duolog", "config.yaml"), "roles:\n  review_intermediate:\n    use:\n      effort: high\n")
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "roles:\n  review_intermediate:\n    use:\n      effort: low\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	role := cfg.Roles["review_intermediate"]
	if role.Use.Effort != "low" || role.Use.Model != "terra" || role.Fallback.Model != "sonnet" || len(cfg.Roles) != 10 {
		t.Fatalf("layers lost or failed to override fields: %+v", cfg.Roles)
	}
	// Without the project layer, the user layer must win over embedded defaults.
	if err := os.Remove(filepath.Join(root, ".duolog", "config.yaml")); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Roles["review_intermediate"].Use.Effort != "high" {
		t.Fatal("user config did not override defaults")
	}
}

func TestLocalOverridesProject(t *testing.T) {
	root := isolatedRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "roles:\n  review_intermediate:\n    use:\n      effort: high\n")
	writeConfig(t, filepath.Join(root, ".duolog", "config.local.yaml"), "roles:\n  review_intermediate:\n    use:\n      effort: low\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Roles["review_intermediate"].Use.Effort != "low" {
		t.Fatal("local layer did not win")
	}
}

func TestEmbeddedDefaultsAreValid(t *testing.T) {
	cfg, err := Load(isolatedRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Roles) != 10 {
		t.Fatalf("got %d roles, want 10", len(cfg.Roles))
	}
	if cfg.Models["sol"].Family != "sol" || cfg.Models["sol"].PricedFor != "gpt-6.1-sol" || cfg.Models["haiku"].Note == "" {
		t.Fatal("model metadata was lost")
	}
	if cfg.Capabilities.OnMissing.Skill != "read_source" || !cfg.Sync.Auto || cfg.Rex.CaptureLines != 20 {
		t.Fatal("default sections were lost")
	}
}

func TestRejectUnknownKey(t *testing.T) {
	root := isolatedRoot(t)
	path := filepath.Join(root, ".duolog", "config.yaml")
	writeConfig(t, path, "roles:\n  plan:\n    use:\n      effrot: low\n")
	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "effrot") || !strings.Contains(err.Error(), path) {
		t.Fatalf("want error naming key and file, got %v", err)
	}
}

func TestRejectRoleWithUnknownModel(t *testing.T) {
	root := isolatedRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "roles:\n  plan:\n    use:\n      model: foo\n")
	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "plan") || !strings.Contains(err.Error(), "foo") {
		t.Fatalf("got %v", err)
	}
}

func TestPinnedModelAccepted(t *testing.T) {
	root := isolatedRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "roles:\n  plan:\n    use: {model: claude-opus-5-5}\n    fallback: {model: gpt-6.1-sol}\n")
	if _, err := Load(root); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultsHoldOnlyAliases(t *testing.T) {
	var cfg Config
	if err := yaml.Unmarshal(defaultConfig, &cfg); err != nil {
		t.Fatal(err)
	}
	for name, role := range cfg.Roles {
		for _, target := range []Target{role.Use, role.Fallback} {
			if _, ok := cfg.Models[target.Model]; !ok {
				t.Errorf("role %s uses non-alias %q", name, target.Model)
			}
		}
	}
}

var mistakeCases = []struct{ name, yaml string }{
	{"effort", "roles:\n  plan:\n    use:\n      effort: hgh\n"},
	{"agent", "roles:\n  plan:\n    use:\n      agent: gemini\n"},
	{"unknown key", "roles:\n  plan:\n    use:\n      effrot: high\n"},
	{"session", "roles:\n  plan:\n    session: maybe\n"},
}

func TestSchemaAndLoadAgree(t *testing.T) {
	for _, tc := range mistakeCases {
		t.Run(tc.name, func(t *testing.T) {
			root := isolatedRoot(t)
			writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), tc.yaml)
			if _, err := Load(root); err == nil {
				t.Fatal("Load accepted invalid configuration")
			}
		})
	}
}

func TestAllEffortsAccepted(t *testing.T) {
	for _, effort := range []string{"low", "medium", "high", "xhigh", "max", "ultra"} {
		t.Run(effort, func(t *testing.T) {
			root := isolatedRoot(t)
			writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "roles:\n  plan:\n    use:\n      effort: "+effort+"\n")
			if _, err := Load(root); err != nil {
				t.Fatal(err)
			}
		})
	}
}
