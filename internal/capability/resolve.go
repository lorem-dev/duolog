package capability

import (
	"fmt"
	"strings"

	"github.com/lorem-dev/duolog/internal/config"
)

const (
	ActionPresent    = "present"
	ActionReadSource = "read_source"
	ActionLeadFirst  = "lead_first"
	ActionReroute    = "reroute"
	ActionRefuse     = "refuse"
)

// NeedsLine is what every worker is told about a tool it cannot find.
const NeedsLine = "If a tool you need is missing, stop and answer done false with its name in needs."

type Resolution struct{ Need, Action, Hint string }

// ParseNeed splits "kind:name"; kind is skill, plugin or mcp.
func ParseNeed(s string) (kind, name string, err error) {
	kind, name, ok := strings.Cut(strings.TrimSpace(s), ":")
	kind, name = strings.ToLower(kind), strings.TrimSpace(name)
	if !ok || name == "" || (kind != KindSkill && kind != KindPlugin && kind != KindMCP) {
		return "", "", fmt.Errorf("need %q: write kind:name with kind one of skill, plugin, mcp", s)
	}
	return kind, name, nil
}

// Resolve decides per need what to do for the target agent. source is the
// agent that has the capability (the lead). An unknown inventory never yields a
// claim of absence: the need is passed on with the line about needs.
func Resolve(target, source Inventory, needs []string, cfg config.Capabilities) []Resolution {
	var out []Resolution
	for _, raw := range needs {
		kind, name, err := ParseNeed(raw)
		if err != nil {
			out = append(out, Resolution{Need: raw, Action: ActionRefuse, Hint: err.Error()})
			continue
		}
		need := kind + ":" + name
		if target.Unknown || source.Unknown {
			out = append(out, Resolution{Need: need, Action: ActionPresent, Hint: NeedsLine})
			continue
		}
		if _, ok := find(target, kind, name); ok {
			out = append(out, Resolution{Need: need, Action: ActionPresent})
			continue
		}
		item, ok := find(source, kind, name)
		if !ok {
			out = append(out, Resolution{Need: need, Action: ActionRefuse, Hint: hint(cfg, kind, name, need+" is missing in "+target.Agent+" and "+source.Agent)})
			continue
		}
		action := map[string]string{KindSkill: cfg.OnMissing.Skill, KindMCP: cfg.OnMissing.MCP, KindPlugin: cfg.OnMissing.Plugin}[kind]
		if action == ActionReadSource && item.Source == "" {
			action = ActionLeadFirst
		}
		var text string
		switch action {
		case ActionReadSource:
			text = fmt.Sprintf("Skill %s is not installed here. Read %s and follow it; tool names there may differ.", name, item.Source)
		case ActionReroute:
			text = need + " is missing in " + target.Agent + "; run this role with " + source.Agent + "."
		case ActionRefuse:
			text = need + " is missing in " + target.Agent + "."
		default:
			action = ActionLeadFirst
			text = need + " is missing in " + target.Agent + ". Do this part first and pass the artifact in Read."
		}
		out = append(out, Resolution{Need: need, Action: action, Hint: hint(cfg, kind, name, text)})
	}
	return out
}

// hint lets a project line replace the default text.
func hint(cfg config.Capabilities, kind, name, def string) string {
	for _, key := range []string{kind + ":" + name, name} {
		if h := cfg.Hints[key]; h != "" {
			return h
		}
	}
	return def
}
