package delegate

import (
	"slices"

	"github.com/lorem-dev/duolog/internal/capability"
	"github.com/lorem-dev/duolog/internal/runner"
	"github.com/lorem-dev/duolog/internal/task"
)

// annotate puts lines at the start of the Limits section.
func annotate(text, lines string) string {
	return task.Annotate(text, lines)
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
func limitOwn(spec runner.Spec, needs []string, tools map[string][]string, workerEnv []string) *runner.MCP {
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
		m = &runner.MCP{}
	} else {
		copied := *m
		m = &copied
	}
	m.CodexArgs = append(append([]string(nil), m.CodexArgs...), codexArgs...)
	m.AllowedTools = append(append([]string(nil), m.AllowedTools...), allowed...)
	return m
}
