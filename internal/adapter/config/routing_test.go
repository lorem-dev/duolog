package config

import (
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
)

// routing is the routing of a config that sets a balance and a margin and
// leaves the agent order at its default.
func routing(balance string, margin float64) config.Routing {
	return config.Routing{Balance: balance, Margin: margin, AgentOrder: []string{"claude", "codex"}}
}

func TestRoutingDefaults(t *testing.T) {
	cfg, err := Load(isolatedRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Routing, routing("pace", .10)) {
		t.Errorf("routing defaults = %+v, want pace and 0.10", cfg.Routing)
	}
	if !reflect.DeepEqual(config.DefaultRouting(), cfg.Routing) {
		t.Errorf("DefaultRouting() = %+v, differs from Load's", config.DefaultRouting())
	}
}

func TestRoutingTrustedLayersMayLoosen(t *testing.T) {
	root := isolatedRoot(t)
	writeConfig(t, userConfigPath(t), "routing: {balance: off, margin: 0.3}\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Routing, routing("off", .3)) || len(cfg.Notices) != 0 {
		t.Errorf("user routing = %+v, notices %v", cfg.Routing, cfg.Notices)
	}
	writeConfig(t, filepath.Join(root, ".duolog", "config.local.yaml"), "routing: {balance: pace, margin: 0.2}\n")
	if cfg, err = Load(root); err != nil || !reflect.DeepEqual(cfg.Routing, routing("pace", .2)) {
		t.Errorf("local routing = %+v, %v", cfg.Routing, err)
	}
}

func TestRoutingProjectCannotLoosen(t *testing.T) {
	// The shared project may not turn balancing off or widen the margin.
	root := isolatedRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "routing: {balance: off, margin: 0.5}\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Routing, config.DefaultRouting()) {
		t.Errorf("project loosened routing: %+v", cfg.Routing)
	}
	want := []string{"routing.margin", "routing.balance"}
	got := noticeKeys(cfg.Notices)
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("notices = %v, want %v", got, want)
	}
	for _, n := range cfg.Notices {
		if !strings.Contains(n.Reason, ".duolog/config.yaml") {
			t.Errorf("notice %+v does not name the project file", n)
		}
	}

	// Under a person who turned balancing off, the project may not turn it on
	// either way round; it can only tighten the margin of a person who has pace.
	root = isolatedRoot(t)
	writeConfig(t, userConfigPath(t), "routing: {balance: off}\n")
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "routing: {balance: pace, margin: 0.05}\n")
	if cfg, err = Load(root); err != nil {
		t.Fatal(err)
	}
	if cfg.Routing.Balance != "pace" || cfg.Routing.Margin != .05 || len(cfg.Notices) != 0 {
		t.Errorf("tightening = %+v, notices %v; want pace and 0.05", cfg.Routing, cfg.Notices)
	}

	// A narrower margin and an unchanged balance are accepted.
	root = isolatedRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "routing: {balance: pace, margin: 0.02}\n")
	if cfg, err = Load(root); err != nil || !reflect.DeepEqual(cfg.Routing, routing("pace", .02)) || len(cfg.Notices) != 0 {
		t.Errorf("project tightening = %+v, %v, notices %v", cfg.Routing, err, cfg.Notices)
	}
}

// TestAgentsProjectCannotSwitchOn: a shared layer may switch an agent off but
// not on, so it cannot make an agent the person never enabled a fallback.
func TestAgentsProjectCannotSwitchOn(t *testing.T) {
	root := isolatedRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "agents: {kestrel: {enabled: true}, codex: {enabled: false}}\nrouting: {agent_order: [kestrel]}\n")
	cfg, err := Load(root, kestrelOptions())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AgentEnabled("kestrel") {
		t.Error("a shared layer switched kestrel on")
	}
	if cfg.AgentEnabled("codex") {
		t.Error("a shared layer could not switch codex off")
	}
	if got := noticeKeys(cfg.Notices); !slices.Equal(got, []string{"agents.kestrel.enabled"}) {
		t.Errorf("notices = %v, want agents.kestrel.enabled", got)
	}
	for _, n := range cfg.Notices {
		if !strings.Contains(n.Reason, ".duolog/config.yaml") {
			t.Errorf("notice %+v does not name the project file", n)
		}
	}

	// The person's own layer may switch it on.
	root = isolatedRoot(t)
	writeConfig(t, userConfigPath(t), "agents: {kestrel: {enabled: true}}\n")
	if cfg, err = Load(root, kestrelOptions()); err != nil || !cfg.AgentEnabled("kestrel") || len(cfg.Notices) != 0 {
		t.Errorf("user switch = %v, %v, notices %v", cfg.AgentEnabled("kestrel"), err, cfg.Notices)
	}

	// A shared true for an agent the layers below already have on widens
	// nothing: it is kept, with no notice.
	root = isolatedRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "agents: {claude: {enabled: true}, codex: {enabled: false}}\n")
	if cfg, err = Load(root, kestrelOptions()); err != nil || !cfg.AgentEnabled("claude") || cfg.AgentEnabled("codex") || len(cfg.Notices) != 0 {
		t.Errorf("shared no-op true: claude %v, codex %v, err %v, notices %v", cfg.AgentEnabled("claude"), cfg.AgentEnabled("codex"), err, cfg.Notices)
	}
}

func TestRoutingRejectsBadValues(t *testing.T) {
	for _, tc := range []struct{ yaml, key string }{
		{"routing: {margin: 1}\n", "routing.margin"},
		{"routing: {margin: -0.1}\n", "routing.margin"},
		{"routing: {balance: always}\n", "balance"},
	} {
		root := isolatedRoot(t)
		writeConfig(t, filepath.Join(root, ".duolog", "config.local.yaml"), tc.yaml)
		if _, err := Load(root); err == nil || !strings.Contains(err.Error(), tc.key) {
			t.Errorf("%q: err = %v, want it to name %s", tc.yaml, err, tc.key)
		}
	}
}
