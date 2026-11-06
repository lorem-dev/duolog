package policy

import (
	"slices"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// heldReadOnly names what holds a worker to reads only: "mode plan" at level
// plan, "role sandbox" in a read-only role, or "" for neither. A Claude
// worker or peer there starts in Claude's default mode, never in its plan
// mode, so the gate alone keeps it read-only (mode.GateHeld).
func heldReadOnly(m mode.Mode, lead LeadRules) string {
	switch {
	case m == mode.Plan:
		return "mode plan"
	case lead.RoleSandbox == "read-only":
		return "role sandbox"
	}
	return ""
}

// unjudged decides an MCP tool (mcpDecision) and any other tool the gate
// cannot judge as a read, a write, a command or a web read; ok is false for
// those it can. Held to reads only (heldReadOnly), such a tool is denied,
// not asked, so a lead's allow cannot run it either.
func unjudged(p config.Policy, a Action, held string, decision func(kind, reason, rule string) Decision) (Decision, bool) {
	switch {
	case a.Kind == "mcp":
		return mcpDecision(p, a, held, decision), true
	case a.Kind == "read", a.Kind == "write", a.Kind == "exec", a.Kind == "net":
		return Decision{}, false
	case held != "":
		return decision(Deny, "only reads are permitted here, and duolog cannot tell what this tool does", held), true
	}
	return decision(Ask, "unknown tool requires approval", "unknown tool"), true
}

// readOnlyMCP are the MCP tools known to only read: CodeGraph's tools that
// read the code index, which the default rules allow. A rule must still
// allow one.
var readOnlyMCP = []string{"codegraph/codegraph_search", "codegraph/codegraph_context", "codegraph/codegraph_explore",
	"codegraph/codegraph_node", "codegraph/codegraph_callers", "codegraph/codegraph_callees", "codegraph/codegraph_impact",
	"codegraph/codegraph_files", "codegraph/codegraph_status"}

// defaultReadOnlyMCP reports a call of a tool in readOnlyMCP, by its exact
// folded name.
func defaultReadOnlyMCP(a Action) bool {
	flat := mcpFlat(a.Target)
	return slices.ContainsFunc(readOnlyMCP, func(name string) bool { return mcpFlat(name) == flat })
}
