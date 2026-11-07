package config

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	validator "github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"

	"github.com/lorem-dev/duolog/internal/core/config"
)

func noticeKeys(notices []config.ConfigNotice) []string {
	keys := make([]string, 0, len(notices))
	for _, n := range notices {
		keys = append(keys, n.Key)
	}
	return keys
}

func userConfigPath(t *testing.T) string {
	t.Helper()
	userDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(userDir, "duolog", "config.yaml")
}

// paidSchema compiles the checked-in schema; TestSchemaFileIsCurrent keeps it
// equal to the generated one.
func paidSchema(t *testing.T) *validator.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "duolog-config.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	c := validator.NewCompiler()
	if err := c.AddResource(config.SchemaURL, doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile(config.SchemaURL)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func schemaValidates(t *testing.T, s *validator.Schema, text string) error {
	t.Helper()
	var doc any
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	return s.Validate(doc)
}

func TestPaidDefaultsAndValidation(t *testing.T) {
	cfg, err := Load(isolatedRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.PaidRuns
	if p.UsableShare != .5 {
		t.Errorf("usable_share = %v, want 0.5", p.UsableShare)
	}
	for agent, env := range map[string][]string{"claude": {"ANTHROPIC_API_KEY"}, "codex": {"OPENAI_API_KEY"}} {
		a := p.Agent(agent)
		if a.Allow || a.Calibrate || a.DailyCapUSD != 0 || a.Account != "auto" || !slices.Equal(a.Env, env) {
			t.Errorf("%s defaults = %+v, want off, cap 0, account auto, env %v", agent, a, env)
		}
	}
	if p.Agent("claude").SubscriptionEndpoints == nil || len(p.Agent("claude").SubscriptionEndpoints) != 0 {
		t.Errorf("claude subscription_endpoints = %#v, want empty", p.Agent("claude").SubscriptionEndpoints)
	}
	if a := p.Agent("gemini"); a.Allow || a.Env != nil {
		t.Errorf("unknown agent = %+v, want the zero value", a)
	}
	if len(cfg.Notices) != 0 {
		t.Errorf("default notices = %v, want none", cfg.Notices)
	}

	// The person's own layer: a value -> the full key its error names.
	cases := map[string]string{
		"paid_runs: {claude: {allow: true}}":                                    "paid_runs.claude.daily_cap_usd",
		"paid_runs: {codex: {allow: true, daily_cap_usd: 0}}":                   "paid_runs.codex.daily_cap_usd",
		"paid_runs: {codex: {daily_cap_usd: -1}}":                               "paid_runs.codex.daily_cap_usd",
		"paid_runs: {claude: {allow: true, daily_cap_usd: .nan}}":               "paid_runs.claude.daily_cap_usd",
		"paid_runs: {claude: {allow: true, daily_cap_usd: .inf}}":               "paid_runs.claude.daily_cap_usd",
		"paid_runs: {codex: {daily_cap_usd: -.inf}}":                            "paid_runs.codex.daily_cap_usd",
		"paid_runs: {usable_share: 0}":                                          "paid_runs.usable_share",
		"paid_runs: {usable_share: -0.1}":                                       "paid_runs.usable_share",
		"paid_runs: {usable_share: 1.5}":                                        "paid_runs.usable_share",
		"paid_runs: {usable_share: .nan}":                                       "paid_runs.usable_share",
		"paid_runs: {usable_share: .inf}":                                       "paid_runs.usable_share",
		"paid_runs: {claude: {account: team}}":                                  "paid_runs.claude.account",
		"paid_runs: {codex: {account: team}}":                                   "paid_runs.codex.account",
		"paid_runs: {gemini: {allow: false}}":                                   "paid_runs.gemini",
		"paid_runs: {claude: {alow: true}}":                                     "paid_runs.claude.alow",
		"paid_runs: {codex: {subscription_endpoints: {corp: https://x.test/}}}": "paid_runs.codex.subscription_endpoints",
		"paid_runs: {claude: {calibrate: true}}":                                "paid_runs.claude.calibrate",
		"paid_runs: {codex: {calibrate: true, daily_cap_usd: 5}}":               "paid_runs.codex.calibrate",
	}
	for text, want := range cases {
		root := isolatedRoot(t)
		writeConfig(t, filepath.Join(root, ".duolog", "config.local.yaml"), text+"\n")
		if _, err := Load(root); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want an error with %q", text, err, want)
		}
	}

	root := isolatedRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog", "config.local.yaml"), "paid_runs: {usable_share: 1, codex: {allow: true, daily_cap_usd: 5, calibrate: true, account: workspace}}\n")
	cfg, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if c := cfg.PaidRuns.Agent("codex"); !c.Allow || c.DailyCapUSD != 5 || !c.Calibrate || c.Account != "workspace" || cfg.PaidRuns.UsableShare != 1 {
		t.Fatalf("valid opt-in = %+v", cfg.PaidRuns)
	}

	s := paidSchema(t)
	for _, text := range []string{
		"paid_runs: {claude: {allow: true}}",
		"paid_runs: {codex: {allow: true, daily_cap_usd: 0}}",
		"paid_runs: {codex: {daily_cap_usd: -1}}",
		"paid_runs: {usable_share: 0}",
		"paid_runs: {usable_share: 1.5}",
		"paid_runs: {claude: {account: team}}",
		"paid_runs: {gemini: {allow: false}}",
		"paid_runs: {claude: {alow: true}}",
		"paid_runs: {codex: {subscription_endpoints: {corp: https://x.test/}}}",
	} {
		if err := schemaValidates(t, s, text); err == nil {
			t.Errorf("schema accepted %s", text)
		}
	}
	for _, text := range []string{
		string(config.DefaultYAML()),
		"paid_runs: {codex: {allow: true, daily_cap_usd: 5}}",
		"paid_runs: {claude: {allow: true, daily_cap_usd: 2.5, calibrate: true}}",
		"paid_runs: {claude: {subscription_endpoints: {corp-gateway: \"https://llm.example.com/claude\"}}}",
		"paid_runs: {usable_share: 0.25, claude: {account: workspace}}",
	} {
		if err := schemaValidates(t, s, text); err != nil {
			t.Errorf("schema rejected %s: %v", text, err)
		}
	}
}

func TestProjectPaidOnlyTightens(t *testing.T) {
	// A cloned repository cannot turn on spending: widening values are
	// ignored and named, while workspace accounting still applies.
	root := isolatedRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), `paid_runs:
  usable_share: 0.8
  claude: {allow: true, daily_cap_usd: 50, calibrate: true, account: personal}
  codex: {account: workspace}
`)
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.PaidRuns
	if c := p.Agent("claude"); p.UsableShare != .5 || c.Allow || c.DailyCapUSD != 0 || c.Calibrate || c.Account != "auto" {
		t.Errorf("widening project values applied: %+v", p)
	}
	if p.Agent("codex").Account != "workspace" {
		t.Errorf("codex account = %q, want workspace", p.Agent("codex").Account)
	}
	want := []string{"paid_runs.usable_share", "paid_runs.claude.allow", "paid_runs.claude.daily_cap_usd", "paid_runs.claude.calibrate", "paid_runs.claude.account"}
	if got := noticeKeys(cfg.Notices); !slices.Equal(got, want) {
		t.Errorf("notice keys = %v, want %v", got, want)
	}
	for _, n := range cfg.Notices {
		if !strings.Contains(n.Reason, ".duolog/config.yaml") {
			t.Errorf("notice %+v does not name the project file", n)
		}
	}

	// The person opted in; the project lowers what it may. Omitted fields
	// keep the person's values: no allow in the project does not mean false.
	root = isolatedRoot(t)
	writeConfig(t, userConfigPath(t), `paid_runs:
  usable_share: 0.6
  claude: {allow: true, daily_cap_usd: 10, calibrate: true}
  codex: {allow: true, daily_cap_usd: 5}
`)
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), `paid_runs:
  usable_share: 0.3
  claude: {allow: true, daily_cap_usd: 4, calibrate: false}
  codex: {allow: false, daily_cap_usd: 8}
`)
	cfg, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	p = cfg.PaidRuns
	if c := p.Agent("claude"); p.UsableShare != .3 || !c.Allow || c.DailyCapUSD != 4 || c.Calibrate {
		t.Errorf("claude = %+v share %v, want allowed, cap 4, no calibration, share 0.3", c, p.UsableShare)
	}
	if c := p.Agent("codex"); c.Allow || c.DailyCapUSD != 5 {
		t.Errorf("codex = %+v, want off with the user's cap 5", c)
	}
	if got := noticeKeys(cfg.Notices); !slices.Equal(got, []string{"paid_runs.codex.daily_cap_usd"}) {
		t.Errorf("notice keys = %v, want the codex cap only", got)
	}

	// The local layer is the person's own and may opt in over the project.
	root = isolatedRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "paid_runs: {claude: {allow: false, daily_cap_usd: 0}}\n")
	writeConfig(t, filepath.Join(root, ".duolog", "config.local.yaml"), "paid_runs: {claude: {allow: true, daily_cap_usd: 3}}\n")
	cfg, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if c := cfg.PaidRuns.Agent("claude"); !c.Allow || c.DailyCapUSD != 3 || len(cfg.Notices) != 0 {
		t.Errorf("local opt-in = %+v, notices %v; want allowed with cap 3", c, cfg.Notices)
	}
}

func TestProjectPaidRulesResolveAliasesBeforeRestricting(t *testing.T) {
	root := isolatedRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), `paid_runs:
  claude: &open {allow: true, daily_cap_usd: 50, calibrate: true}
  codex:
    <<: *open
    account: workspace
`)
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "codex"} {
		if a := cfg.PaidRuns.Agent(agent); a.Allow || a.DailyCapUSD != 0 || a.Calibrate {
			t.Errorf("%s = %+v, want the aliased opt-in ignored", agent, a)
		}
	}
	if cfg.PaidRuns.Agent("codex").Account != "workspace" {
		t.Errorf("codex account = %q, want workspace", cfg.PaidRuns.Agent("codex").Account)
	}
	want := []string{
		"paid_runs.claude.allow", "paid_runs.claude.daily_cap_usd", "paid_runs.claude.calibrate",
		"paid_runs.codex.allow", "paid_runs.codex.daily_cap_usd", "paid_runs.codex.calibrate",
	}
	if got := noticeKeys(cfg.Notices); !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))) {
		t.Errorf("notice keys = %v, want %v", got, want)
	}

	// Tightening through an alias and a merge sequence applies; the earlier
	// map wins, and a looser key it adds is still ignored.
	root = isolatedRoot(t)
	writeConfig(t, userConfigPath(t), "paid_runs: {claude: {allow: true, daily_cap_usd: 10}, codex: {allow: true, daily_cap_usd: 10}}\n")
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), `paid_runs:
  claude: &tight {daily_cap_usd: 2, allow: false}
  codex:
    <<: [*tight, {allow: true, daily_cap_usd: 20, calibrate: true}]
`)
	cfg, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "codex"} {
		if a := cfg.PaidRuns.Agent(agent); a.Allow || a.DailyCapUSD != 2 || a.Calibrate {
			t.Errorf("%s = %+v, want off with cap 2", agent, a)
		}
	}
	if got := noticeKeys(cfg.Notices); !slices.Equal(got, []string{"paid_runs.codex.calibrate"}) {
		t.Errorf("notice keys = %v, want the merged calibrate only", got)
	}
}

func TestProjectCannotTrustEndpointOrExpandPaidEnv(t *testing.T) {
	root := isolatedRoot(t)
	writeConfig(t, userConfigPath(t), `paid_runs:
  claude:
    env: [ANTHROPIC_API_KEY, ANTHROPIC_AUTH_TOKEN]
    subscription_endpoints: {corp: "https://llm.example.com/claude", lab: "https://lab.example.com/claude"}
  codex: {env: [OPENAI_API_KEY]}
`)
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), `paid_runs:
  claude:
    env: [ANTHROPIC_AUTH_TOKEN, ANTHROPIC_BASE_URL]
    subscription_endpoints: {corp: "https://llm.example.com/claude", rogue: "https://rogue.example.com", lab: "https://other.example.com/claude"}
  codex: {env: [OPENAI_API_KEY, CODEX_API_KEY]}
`)
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.PaidRuns
	if !slices.Equal(p.Agent("claude").Env, []string{"ANTHROPIC_AUTH_TOKEN"}) || !slices.Equal(p.Agent("codex").Env, []string{"OPENAI_API_KEY"}) {
		t.Errorf("env claude %v codex %v, want the intersections", p.Agent("claude").Env, p.Agent("codex").Env)
	}
	if want := map[string]string{"corp": "https://llm.example.com/claude"}; !maps.Equal(p.Agent("claude").SubscriptionEndpoints, want) {
		t.Errorf("endpoints = %v, want %v", p.Agent("claude").SubscriptionEndpoints, want)
	}
	want := []string{"paid_runs.claude.env", "paid_runs.claude.subscription_endpoints.rogue", "paid_runs.claude.subscription_endpoints.lab", "paid_runs.codex.env"}
	if got := noticeKeys(cfg.Notices); !slices.Equal(got, want) {
		t.Errorf("notice keys = %v, want %v", got, want)
	}
	for _, n := range cfg.Notices {
		if strings.Contains(n.Reason, "example.com") {
			t.Errorf("notice %+v shows an endpoint value", n)
		}
	}
	if !strings.Contains(cfg.Notices[0].Reason, "ANTHROPIC_BASE_URL") || !strings.Contains(cfg.Notices[3].Reason, "CODEX_API_KEY") {
		t.Errorf("env notices do not name the dropped variable: %+v", cfg.Notices)
	}

	// Against the defaults, a project adds no endpoint and no variable, and
	// an empty list removes them. The local layer may add both.
	root = isolatedRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), `paid_runs:
  claude:
    env: [ANTHROPIC_API_KEY, ANTHROPIC_BASE_URL]
    subscription_endpoints: {corp: "https://llm.example.com/claude"}
  codex: {env: []}
`)
	cfg, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	p = cfg.PaidRuns
	if !slices.Equal(p.Agent("claude").Env, []string{"ANTHROPIC_API_KEY"}) || len(p.Agent("codex").Env) != 0 || len(p.Agent("claude").SubscriptionEndpoints) != 0 {
		t.Errorf("project additions applied: %+v", p)
	}
	writeConfig(t, filepath.Join(root, ".duolog", "config.local.yaml"), "paid_runs: {claude: {subscription_endpoints: {corp: \"https://llm.example.com/claude\"}}, codex: {env: [CODEX_API_KEY]}}\n")
	cfg, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if p = cfg.PaidRuns; len(p.Agent("claude").SubscriptionEndpoints) != 1 || !slices.Equal(p.Agent("codex").Env, []string{"CODEX_API_KEY"}) {
		t.Errorf("local additions lost: %+v", p)
	}
}
