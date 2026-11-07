package config

import (
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/agent"
)

// kestrelOptions registers a third agent beside the two built-in ones.
func kestrelOptions() LoadOptions {
	agents := append(builtinAgents(), agent.Descriptor{Name: "kestrel", Display: "Kestrel"})
	return LoadOptions{Agents: agents}
}

// TestUnknownAgentKeyRefused: a key for an agent that is not registered is
// refused with the unknown-key error of any other unknown key.
func TestUnknownAgentKeyRefused(t *testing.T) {
	for _, tc := range []struct{ yaml, want string }{
		{"paid_runs: {kestrel: {allow: true}}\n", "config.paid_runs.kestrel: unknown key"},
		{"coauthors: {kestrel: \"X <x@example.invalid>\"}\n", "config.coauthors.kestrel: unknown key"},
		{"worker_modes: {roles: {implement: {kestrel: plan}}}\n", "config.worker_modes.roles.implement.kestrel: unknown key"},
		{"agents: {kestrel: {enabled: true}}\n", "config.agents.kestrel: unknown key"},
		{"sync: {agents: {model_rows: [{claude: opus, kestrel: kite}]}}\n", "sync.agents.model_rows[0].kestrel: unknown key"},
		{"paid_runs: {codex: {subscription_endpoints: {corp: \"https://x.example.invalid/\"}}}\n", "config.paid_runs.codex.subscription_endpoints: unknown key"},
	} {
		root := isolatedRoot(t)
		writeConfig(t, userConfigPath(t), tc.yaml)
		if _, err := Load(root); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", strings.TrimSpace(tc.yaml), err, tc.want)
		}
	}
	// The same key loads once the agent is registered.
	root := isolatedRoot(t)
	writeConfig(t, userConfigPath(t), "paid_runs: {kestrel: {allow: true, daily_cap_usd: 1}}\ncoauthors: {kestrel: \"K <k@example.invalid>\"}\nagents: {kestrel: {enabled: true}}\n")
	cfg, err := Load(root, kestrelOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.PaidRuns.Agent("kestrel").Allow || cfg.PaidRuns.Agent("kestrel").Account != "auto" || cfg.Coauthors.Templates["kestrel"] == "" || !cfg.AgentEnabled("kestrel") {
		t.Errorf("kestrel settings not read: %+v %+v %+v", cfg.PaidRuns, cfg.Coauthors, cfg.Agents)
	}
}

// TestModelRows: model_map is read as rows, and a model_rows row that
// disagrees with it on a pair fails to load.
func TestModelRows(t *testing.T) {
	root := isolatedRoot(t)
	writeConfig(t, userConfigPath(t), "sync: {agents: {model_map: {opus: sol}}}\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	sync := cfg.Sync.Agents
	if got, err := sync.MapModel("claude", "opus", "codex"); err != nil || got != "sol" {
		t.Errorf("opus on codex = %q, %v; want sol", got, err)
	}
	if got, err := sync.MapModel("codex", "sol", "claude"); err != nil || got != "opus" {
		t.Errorf("sol on claude = %q, %v; want opus", got, err)
	}
	if _, err := sync.MapModel("codex", "nosuch", "claude"); err == nil || !strings.Contains(err.Error(), "no model mapping for nosuch on claude") {
		t.Errorf("unmapped alias: %v", err)
	}

	root = isolatedRoot(t)
	writeConfig(t, userConfigPath(t), "sync: {agents: {model_map: {opus: sol}, model_rows: [{claude: opus, codex: terra}]}}\n")
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "model_map and model_rows must agree") {
		t.Errorf("disagreeing rows: %v", err)
	}

	// A row that agrees, or adds a third agent's alias, loads.
	root = isolatedRoot(t)
	writeConfig(t, userConfigPath(t), "sync: {agents: {model_rows: [{claude: opus, codex: sol, kestrel: kite}]}}\n")
	cfg, err = Load(root, kestrelOptions())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := cfg.Sync.Agents.MapModel("kestrel", "kite", "claude"); err != nil || got != "opus" {
		t.Errorf("kite on claude = %q, %v; want opus", got, err)
	}
}

// TestAgentsEnabledDefault: the built-in agents are on with no agents key; a
// registered third agent is off until its entry switches it on.
func TestAgentsEnabledDefault(t *testing.T) {
	cfg, err := Load(isolatedRoot(t), kestrelOptions())
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{"claude": true, "codex": true, "kestrel": false} {
		if !cfg.AgentRegistered(name) || cfg.AgentEnabled(name) != want {
			t.Errorf("agent %s: registered %v, enabled %v, want enabled %v", name, cfg.AgentRegistered(name), cfg.AgentEnabled(name), want)
		}
	}
	if cfg.AgentRegistered("nosuch") || cfg.AgentEnabled("nosuch") {
		t.Error("an unregistered agent counts as registered or enabled")
	}
	root := isolatedRoot(t)
	writeConfig(t, userConfigPath(t), "agents: {codex: {enabled: false}, kestrel: {enabled: true}}\n")
	if cfg, err = Load(root, kestrelOptions()); err != nil || cfg.AgentEnabled("codex") || !cfg.AgentEnabled("kestrel") || !cfg.AgentEnabled("claude") {
		t.Errorf("switches not read: %+v, %v", cfg.Agents, err)
	}
}
