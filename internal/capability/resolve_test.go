package capability

import (
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
)

func defaults() config.Capabilities {
	return config.Capabilities{OnMissing: config.MissingCapabilities{Skill: ActionReadSource, MCP: ActionLeadFirst, Plugin: ActionLeadFirst}}
}
func inv(agent string, items ...Item) Inventory { return Inventory{Agent: agent, Items: items} }

var (
	lead   = inv("claude", Item{Kind: "skill", Name: "figma-use", Source: "/abs/figma-use/SKILL.md"}, Item{Kind: "mcp", Name: "codegraph"})
	worker = inv("codex")
)

func one(t *testing.T, target, source Inventory, need string, cfg config.Capabilities) Resolution {
	t.Helper()
	r := Resolve(target, source, []string{need}, cfg)
	if len(r) != 1 {
		t.Fatal(r)
	}
	return r[0]
}

func TestPresentNeedAddsNothing(t *testing.T) {
	r := one(t, lead, worker, "skill:figma-use", defaults())
	if r.Action != ActionPresent || r.Hint != "" {
		t.Fatal(r)
	}
}
func TestMissingSkillGivesSourcePath(t *testing.T) {
	r := one(t, worker, lead, "skill:figma-use", defaults())
	if r.Action != ActionReadSource || !strings.Contains(r.Hint, "/abs/figma-use/SKILL.md") || !strings.Contains(r.Hint, "tool names there may differ") {
		t.Fatal(r)
	}
}
func TestMissingSkillNowhereRefuses(t *testing.T) {
	r := one(t, worker, lead, "skill:nope", defaults())
	if r.Action != ActionRefuse || !strings.Contains(r.Hint, "skill:nope") {
		t.Fatal(r)
	}
}
func TestMissingMcpIsLeadFirst(t *testing.T) {
	r := one(t, worker, lead, "mcp:codegraph", defaults())
	if r.Action != ActionLeadFirst || !strings.Contains(r.Hint, "Do this part first") || !strings.Contains(r.Hint, "artifact in Read") {
		t.Fatal(r)
	}
}
func TestRerouteSetting(t *testing.T) {
	cfg := defaults()
	cfg.OnMissing.MCP = ActionReroute
	r := one(t, worker, lead, "mcp:codegraph", cfg)
	if r.Action != ActionReroute || !strings.Contains(r.Hint, "claude") {
		t.Fatal(r)
	}
}
func TestRefuseSetting(t *testing.T) {
	cfg := defaults()
	cfg.OnMissing.Skill = ActionRefuse
	if r := one(t, worker, lead, "skill:figma-use", cfg); r.Action != ActionRefuse {
		t.Fatal(r)
	}
}
func TestProjectHintWins(t *testing.T) {
	cfg := defaults()
	cfg.Hints = map[string]string{"codegraph": "Ask the lead for a graph dump."}
	r := one(t, worker, lead, "mcp:codegraph", cfg)
	if r.Action != ActionLeadFirst || r.Hint != "Ask the lead for a graph dump." {
		t.Fatal(r)
	}
}
func TestSymmetric(t *testing.T) {
	codexLead := inv("codex", Item{Kind: "skill", Name: "figma-use", Source: "/abs/figma-use/SKILL.md"}, Item{Kind: "mcp", Name: "codegraph"})
	claudeWorker := inv("claude")
	a := one(t, worker, lead, "skill:figma-use", defaults())
	b := one(t, claudeWorker, codexLead, "skill:figma-use", defaults())
	if a != b {
		t.Fatal(a, b)
	}
	if one(t, claudeWorker, codexLead, "mcp:codegraph", defaults()).Action != ActionLeadFirst {
		t.Fatal("mcp")
	}
}
func TestUnknownInventoryClaimsNothing(t *testing.T) {
	r := one(t, Inventory{Agent: "codex", Unknown: true}, lead, "mcp:codegraph", defaults())
	if r.Action != ActionPresent || r.Hint != NeedsLine {
		t.Fatal(r)
	}
}
func TestParseNeed(t *testing.T) {
	if _, _, err := ParseNeed("figma"); err == nil || !strings.Contains(err.Error(), "skill, plugin, mcp") {
		t.Fatal(err)
	}
	if k, n, err := ParseNeed("MCP:codegraph"); err != nil || k != "mcp" || n != "codegraph" {
		t.Fatal(k, n, err)
	}
}
