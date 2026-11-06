package delegate

import (
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/capability"
	"github.com/lorem-dev/duolog/internal/core/run"
	"github.com/lorem-dev/duolog/internal/core/taskspec"
)

// capPlan is what the capability check asks of a run: the MCP servers to
// bridge, the needs the worker lacks, the notes for the task copy, whether to
// try the other agent, and the first resolution that refuses the task.
type capPlan struct {
	Bridge, Lacking, Notes []string
	Reroute                bool
	Refuse                 *capability.Resolution
}

// capabilityPlan sorts the resolutions of a task's needs by their action. Only
// the first refusal is kept: a refusal ends the run.
func capabilityPlan(res []capability.Resolution) capPlan {
	var p capPlan
	for _, r := range res {
		switch r.Action {
		case capability.ActionBridge:
			p.Bridge = append(p.Bridge, strings.TrimPrefix(r.Need, capability.KindMCP+":"))
		case capability.ActionRefuse:
			if p.Refuse == nil {
				p.Refuse = &r
			}
		case capability.ActionLeadFirst:
			p.Lacking = append(p.Lacking, r.Need)
			p.Notes = append(p.Notes, r.Hint)
		case capability.ActionReroute:
			p.Reroute = true
			p.Notes = append(p.Notes, r.Hint)
		default:
			if r.Hint != "" {
				p.Notes = append(p.Notes, r.Hint)
			}
		}
	}
	return p
}

// annotate puts lines at the start of the Limits section.
func annotate(text, lines string) string {
	return taskspec.Annotate(text, lines)
}

// needsKind reports whether a task names a need of one of these kinds. A Claude
// worker keeps MCP servers only for an mcp or plugin need, since a plugin can
// bring a server, and a minimal worker keeps skills only for a skill or plugin
// need.
func needsKind(needs []string, kinds ...string) bool {
	for _, need := range needs {
		if kind, _, err := capability.ParseNeed(need); err == nil && slices.Contains(kinds, kind) {
			return true
		}
	}
	return false
}

// limitOwn adds capabilities.mcp_tools for the worker's own servers that the
// task needs, so that the list filters them as it filters a bridged server.
// The worker's servers are those of workerEnv, its environment.
func limitOwn(spec run.Spec, needs []string, tools map[string][]string, workerEnv []string) *run.MCP {
	var own []string
	for _, need := range withoutBridged(needs, spec.MCP) {
		if kind, name, err := capability.ParseNeed(need); err == nil && kind == capability.KindMCP {
			own = append(own, name)
		}
	}
	if spec.Route.Agent == "codex" {
		// enabled_tools for a server Codex does not define, such as a plugin's,
		// would make a table with no transport, which Codex refuses.
		defs := capability.ServersEnv("codex", spec.Cwd, workerEnv)
		own = slices.DeleteFunc(own, func(name string) bool { _, ok := defs[name]; return !ok })
	}
	codexArgs, allowed := capability.Limit(spec.Route.Agent, own, tools)
	m := spec.MCP
	if m == nil {
		if len(codexArgs) == 0 && len(allowed) == 0 {
			return nil
		}
		m = &run.MCP{}
	} else {
		copied := *m
		m = &copied
	}
	m.CodexArgs = append(append([]string(nil), m.CodexArgs...), codexArgs...)
	m.AllowedTools = append(append([]string(nil), m.AllowedTools...), allowed...)
	return m
}
