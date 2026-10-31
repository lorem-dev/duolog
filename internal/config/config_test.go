package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
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

func TestRejectMergedInvalidEnums(t *testing.T) {
	for name, contents := range map[string]string{
		"inline rule":    "policy:\n  rules:\n    - <<: {decision: invalid}\n",
		"alias rule":     "capabilities:\n  hints: &rule {decision: invalid}\npolicy:\n  rules:\n    - <<: *rule\n",
		"merge sequence": "policy:\n  rules:\n    - <<: [{decision: invalid}, {decision: allow}]\n",
		"nested merge":   "roles:\n  plan:\n    <<: {use: {<<: {effort: invalid}}}\n",
		"map entry":      "models:\n  sol:\n    <<: {agent: invalid}\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := isolatedRoot(t)
			path := filepath.Join(root, ".duolog", "config.yaml")
			writeConfig(t, path, contents)
			if _, err := Load(root); err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("want invalid enum error naming file, got %v", err)
			}
		})
	}
}

func TestYAMLMergePrecedence(t *testing.T) {
	root := isolatedRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), `policy:
  rules:
    - &rule {decision: allow, action: read}
    - <<: *rule
      decision: deny
    - <<: [{decision: ask}, {decision: invalid}]
roles:
  plan:
    use:
      <<: {effort: invalid}
      effort: low
`)
	writeConfig(t, filepath.Join(root, ".duolog", "config.local.yaml"), "roles:\n  plan:\n    use:\n      <<: {effort: medium}\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Policy.Rules[1].Decision != "deny" || cfg.Policy.Rules[1].Action != "read" || cfg.Policy.Rules[2].Decision != "ask" {
		t.Fatalf("YAML merge precedence lost: %+v", cfg.Policy.Rules)
	}
	if cfg.Roles["plan"].Use.Effort != "medium" || cfg.Roles["plan"].Use.Model != "opus" {
		t.Fatalf("merged override lost inherited fields: %+v", cfg.Roles["plan"].Use)
	}
}

func TestRejectNullOverrides(t *testing.T) {
	for name, contents := range map[string]string{
		"root":          "null\n",
		"policy":        "policy: null\n",
		"boolean":       "policy: {inherit_lead: null}\n",
		"integer":       "policy: {ask_timeout_seconds: ~}\n",
		"string":        "wire: {language: null}\n",
		"enum":          "policy: {on_unmatched: null}\n",
		"slice":         "policy: {rules: null}\n",
		"rule":          "policy: {rules: [null]}\n",
		"slice element": "env_passthrough: [null]\n",
		"map":           "models: null\n",
		"map entry":     "models: {sol: null}\n",
		"role target":   "roles: {plan: {use: null}}\n",
		"merged null":   "policy: {<<: {inherit_lead: null}}\n",
		"aliased null":  "capabilities: {hints: {first: &empty null, second: *empty}}\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := isolatedRoot(t)
			path := filepath.Join(root, ".duolog", "config.yaml")
			writeConfig(t, path, contents)
			if _, err := Load(root); err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "null") {
				t.Fatalf("want null error naming file, got %v", err)
			}
		})
	}
}

func TestStringScalarsPreserveOriginalValues(t *testing.T) {
	for _, scalar := range []string{"012", "2020-01-01", "1e3", "0x10", "true"} {
		for name, contents := range map[string]string{
			"direct":                "wire: {language: " + scalar + "}\n",
			"alias":                 "capabilities: {hints: {value: &value " + scalar + "}}\nwire: {language: *value}\n",
			"merge":                 "wire: {<<: {language: " + scalar + "}}\n",
			"nested merge sequence": "wire: {<<: [{<<: {language: " + scalar + "}}, {language: ignored}]}\n",
		} {
			t.Run(scalar+"/"+name, func(t *testing.T) {
				root := isolatedRoot(t)
				writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "wire: {language: project}\n")
				writeConfig(t, filepath.Join(root, ".duolog", "config.local.yaml"), contents)
				cfg, err := Load(root)
				if err != nil {
					t.Fatal(err)
				}
				if cfg.Wire.Language != scalar {
					t.Fatalf("language = %q, want original scalar %q", cfg.Wire.Language, scalar)
				}
			})
		}
	}
}

// macOS finds the Claude login through USER; a worker without it reports
// "Not logged in" (found by the live test round).
func TestDefaultPassthroughKeepsLoginVariables(t *testing.T) {
	cfg, err := Load(isolatedRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"PATH", "HOME", "USER"} {
		if !slices.Contains(cfg.EnvPassthrough, want) {
			t.Errorf("env_passthrough lacks %s: %v", want, cfg.EnvPassthrough)
		}
	}
}
