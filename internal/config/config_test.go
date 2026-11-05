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
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("APPDATA", home)
	return t.TempDir()
}

func TestDefaultModelsMatchLoadedDefaults(t *testing.T) {
	models, err := DefaultModels()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(isolatedRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) == 0 || len(models) != len(cfg.Models) {
		t.Fatalf("DefaultModels %v, Load gives %v", models, cfg.Models)
	}
	for name, m := range models {
		if cfg.Models[name] != m {
			t.Errorf("model %s: DefaultModels %+v, Load %+v", name, m, cfg.Models[name])
		}
	}
	if models["opus"].Agent != "claude" || models["sol"].Agent != "codex" || models["sol"].Family != "sol" {
		t.Fatalf("models %v", models)
	}
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
	if role.Use.Effort != "low" || role.Use.Model != "sol" || role.Fallback.Model != "sonnet" || len(cfg.Roles) != 12 {
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
	if len(cfg.Roles) != 12 {
		t.Fatalf("got %d roles, want 12", len(cfg.Roles))
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
		"boolean":       "policy: {on_unmatched: null}\n",
		"integer":       "policy: {ask_timeout_seconds: ~}\n",
		"string":        "policy: {on_unmatched: null}\n",
		"enum":          "policy: {on_unmatched: null}\n",
		"slice":         "policy: {rules: null}\n",
		"rule":          "policy: {rules: [null]}\n",
		"slice element": "env_passthrough: [null]\n",
		"map":           "models: null\n",
		"map entry":     "models: {sol: null}\n",
		"role target":   "roles: {plan: {use: null}}\n",
		"merged null":   "policy: {<<: {on_unmatched: null}}\n",
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
			"direct":                "capabilities: {hints: {note: " + scalar + "}}\n",
			"alias":                 "capabilities: {hints: {value: &value " + scalar + ", note: *value}}\n",
			"merge":                 "capabilities: {hints: {<<: {note: " + scalar + "}}}\n",
			"nested merge sequence": "capabilities: {hints: {<<: [{<<: {note: " + scalar + "}}, {note: ignored}]}}\n",
		} {
			t.Run(scalar+"/"+name, func(t *testing.T) {
				root := isolatedRoot(t)
				writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "capabilities: {hints: {note: project}}\n")
				writeConfig(t, filepath.Join(root, ".duolog", "config.local.yaml"), contents)
				cfg, err := Load(root)
				if err != nil {
					t.Fatal(err)
				}
				if got := cfg.Capabilities.Hints["note"]; got != scalar {
					t.Fatalf("note = %q, want original scalar %q", got, scalar)
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

func TestCRLFConfig(t *testing.T) {
	root := isolatedRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "version: 1\r\npolicy:\r\n  on_unmatched: deny\r\n")
	cfg, err := Load(root)
	if err != nil || cfg.Policy.OnUnmatched != "deny" {
		t.Fatal(cfg, err)
	}
}

func TestProjectRulesCannotDropDefaultDenies(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("APPDATA", home)
	if err := os.MkdirAll(filepath.Join(root, ".duolog"), 0o755); err != nil {
		t.Fatal(err)
	}
	project := "policy:\n  rules:\n    - {action: exec, match: \"pytest *\", decision: allow, roles: [tests]}\n"
	if err := os.WriteFile(filepath.Join(root, ".duolog", "config.yaml"), []byte(project), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Policy.Rules[0].Match != "pytest *" {
		t.Fatalf("project rule lost: %+v", cfg.Policy.Rules[0])
	}
	found := map[string]bool{}
	for _, rule := range cfg.Policy.Rules {
		if rule.Decision == "deny" {
			found[rule.Match] = true
		}
	}
	for _, want := range []string{"{**/.duolog/**,**/.git,**/.git/**}", "git {push,reset --hard,clean,rebase}*", "rm -rf *"} {
		if !found[want] {
			t.Errorf("default deny %q was dropped", want)
		}
	}
}

// Bridging hands credentials to the other vendor's agent, so it is opt-in.
func TestMCPBridgeIsOptIn(t *testing.T) {
	root := isolatedRoot(t)
	cfg, err := Load(root)
	if err != nil || cfg.Capabilities.OnMissing.MCP != "lead_first" || len(cfg.Capabilities.MCPTools) != 0 {
		t.Fatal(cfg.Capabilities, err)
	}
	path := filepath.Join(root, ".duolog", "config.yaml")
	writeConfig(t, path, "capabilities:\n  on_missing: {mcp: bridge}\n  mcp_tools: {jira: [get_issue, search]}\n")
	cfg, err = Load(root)
	if err != nil || cfg.Capabilities.OnMissing.MCP != "bridge" || cfg.Capabilities.OnMissing.Skill != "read_source" || strings.Join(cfg.Capabilities.MCPTools["jira"], ",") != "get_issue,search" {
		t.Fatal(cfg.Capabilities, err)
	}
	writeConfig(t, path, "capabilities:\n  on_missing: {mcp: brige}\n")
	if _, err = Load(root); err == nil || !strings.Contains(err.Error(), "brige") {
		t.Fatal(err)
	}
}

func TestAuthFailureDefaults(t *testing.T) {
	root := isolatedRoot(t)
	cfg, err := Load(root)
	if err != nil || cfg.Capabilities.OnAuthFailure != "login_then_lead" || cfg.Capabilities.LoginTimeoutSeconds != 180 {
		t.Fatal(cfg.Capabilities, err)
	}
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "capabilities:\n  on_auth_failure: retry\n")
	if _, err = Load(root); err == nil || !strings.Contains(err.Error(), "retry") {
		t.Fatal(err)
	}
}

// mcp_tools and remembered MCP tools add gate allows to the policy; they do
// not replace the default rules.
func TestMCPRulesAddToPolicy(t *testing.T) {
	root := isolatedRoot(t)
	base, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "capabilities:\n  mcp_tools: {jira: [get_issue, search]}\n")
	writeConfig(t, filepath.Join(root, ".duolog", LearnedFile), "rules:\n  - {action: mcp, match: wiki/read, decision: allow}\n  - {action: mcp, match: wiki/delete, decision: deny}\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Policy.Rules) != len(base.Policy.Rules)+3 {
		t.Fatal(len(cfg.Policy.Rules), len(base.Policy.Rules))
	}
	var got []string
	for _, r := range cfg.Policy.Rules[len(base.Policy.Rules):] {
		got = append(got, r.Action+" "+r.Match+" "+r.Decision)
	}
	if strings.Join(got, "; ") != "mcp wiki/read allow; mcp jira/get_issue allow; mcp jira/search allow" {
		t.Fatal(got)
	}
}

func TestAutoAnswers(t *testing.T) {
	root := isolatedRoot(t)
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Rex.AutoAnswers) != 1 || cfg.Rex.AutoAnswers[0].Match != "Hooks need review" {
		t.Fatalf("%+v", cfg.Rex)
	}
	for _, answer := range []string{"Trust all", "Yes, and don't ask again", "Always allow", " trust folder"} {
		writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "rex:\n  auto_answers:\n    - match: question\n      answer: "+answer+"\n")
		if _, err := Load(root); err == nil {
			t.Fatalf("accepted %q", answer)
		}
	}
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "rex:\n  auto_answers: []\n")
	cfg, err = Load(root)
	if err != nil || len(cfg.Rex.AutoAnswers) != 0 {
		t.Fatalf("%+v %v", cfg, err)
	}
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "rex:\n  auto_answers:\n    - match: question\n      answer: No\n")
	cfg, err = Load(root)
	if err != nil || len(cfg.Rex.AutoAnswers) != 1 || cfg.Rex.AutoAnswers[0].Match != "question" {
		t.Fatalf("%+v %v", cfg, err)
	}
}
