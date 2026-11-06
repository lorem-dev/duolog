package config_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/gate"
)

func TestDefaultCodegraphReadTools(t *testing.T) {
	home := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(key, home)
	}
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tools := []string{"codegraph_search", "codegraph_context", "codegraph_explore", "codegraph_node", "codegraph_callers", "codegraph_callees", "codegraph_impact", "codegraph_files", "codegraph_status"}
	for _, tool := range tools {
		count := 0
		for _, rule := range cfg.Policy.Rules {
			if rule.Action == "mcp" && rule.Match == "codegraph/"+tool {
				count++
				if rule.Decision != "allow" || len(rule.Roles) != 0 || strings.Contains(rule.Match, "*") {
					t.Fatalf("invalid rule: %+v", rule)
				}
			}
		}
		if count != 1 {
			t.Errorf("%s has %d default rules", tool, count)
		}
		for role := range cfg.Roles {
			for _, agent := range []string{"claude", "codex"} {
				a, err := gate.Normalize(agent, []byte(fmt.Sprintf(`{"tool_name":"mcp__codegraph__%s","tool_input":{},"mcp_server":{"name":"codegraph"}}`, tool)))
				if err != nil {
					t.Fatal(err)
				}
				a.Role = role
				d := gate.Evaluate(cfg.Policy, gate.LeadRules{}, mode.Ask, a)
				if d.Kind != gate.Allow {
					t.Errorf("%s %s %s: %+v", agent, role, tool, d)
				}
			}
		}
	}
	for _, target := range []string{"codegraph/codegraph_init", "codegraph/codegraph_delete"} {
		if d := gate.Evaluate(cfg.Policy, gate.LeadRules{}, mode.Ask, gate.Action{Kind: "mcp", Target: target}); d.Kind != gate.Ask {
			t.Errorf("unlisted tool %s: %+v", target, d)
		}
	}
}
