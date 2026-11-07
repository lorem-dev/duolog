package delegate

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/caps"
	"github.com/lorem-dev/duolog/internal/core/run"
	"github.com/lorem-dev/duolog/internal/core/taskspec"
)

// capPlan is what the capability check asks of a run: the MCP servers to
// bridge, the needs the worker lacks, the notes for the task copy, whether to
// try the other agent, and the first resolution that refuses the task.
type capPlan struct {
	Bridge, Lacking, Notes []string
	Reroute                bool
	Refuse                 *caps.Resolution
}

// capabilityPlan sorts the resolutions of a task's needs by their action. Only
// the first refusal is kept: a refusal ends the run.
func capabilityPlan(res []caps.Resolution) capPlan {
	var p capPlan
	for _, r := range res {
		switch r.Action {
		case caps.ActionBridge:
			p.Bridge = append(p.Bridge, strings.TrimPrefix(r.Need, caps.KindMCP+":"))
		case caps.ActionRefuse:
			if p.Refuse == nil {
				p.Refuse = &r
			}
		case caps.ActionLeadFirst:
			p.Lacking = append(p.Lacking, r.Need)
			p.Notes = append(p.Notes, r.Hint)
		case caps.ActionReroute:
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
		if kind, _, err := caps.ParseNeed(need); err == nil && slices.Contains(kinds, kind) {
			return true
		}
	}
	return false
}

// scan lists what agent has for a process whose environment is environ. An
// agent without Caps has an unknown inventory.
func (d Deps) scan(ctx context.Context, agent, root string, environ []string) (caps.Inventory, error) {
	c := d.Agents[agent].Caps
	if c == nil {
		return caps.Inventory{Agent: agent, Unknown: true}, nil
	}
	return c.Scan(ctx, root, environ)
}

// leadInventory lists what the lead's agent has, from duolog's own
// environment, and marks each of its config files' servers with whether a
// worker of agent worker could borrow it, judged from duolog's environment
// too; Bridge checks again with the worker's.
func (d Deps) leadInventory(ctx context.Context, lead, worker, root string) (caps.Inventory, error) {
	inv, err := d.scan(ctx, lead, root, os.Environ())
	if err != nil || inv.Unknown {
		return inv, err
	}
	to := d.Agents[worker].Caps
	why := func(s caps.Server) string {
		if to == nil {
			return fmt.Sprintf("agent %s is unknown", worker)
		}
		return to.Refusal(root, s, os.Environ())
	}
	inv.Items = caps.Mark(inv.Items, d.servers(lead, root, os.Environ()), why)
	return inv, nil
}

// servers are the MCP servers agent defines for a process whose environment
// is environ; an agent without Caps defines none.
func (d Deps) servers(agent, root string, environ []string) map[string]caps.Server {
	if c := d.Agents[agent].Caps; c != nil {
		return c.Servers(root, environ)
	}
	return nil
}

// bridge hands the named servers of the lead's agent, from duolog's own
// environment, to a worker of agent to whose environment is workerEnv. A
// server that cannot be bridged is left out, with the reason in failed.
func (d Deps) bridge(from, to, root string, names []string, tools map[string][]string, workerEnv []string) (*run.MCP, map[string]string) {
	failed := map[string]string{}
	defined := d.servers(from, root, os.Environ())
	picked := map[string]caps.Server{}
	for _, name := range names {
		if s, ok := defined[name]; ok {
			picked[name] = s
		} else {
			failed[name] = "the lead's config does not define it"
		}
	}
	c := d.Agents[to].Caps
	if c == nil {
		for name := range picked {
			failed[name] = fmt.Sprintf("agent %s is unknown", to)
		}
		return nil, failed
	}
	m, more := c.Import(root, picked, tools, workerEnv)
	for name, why := range more {
		failed[name] = why
	}
	return m, failed
}

// limitOwn adds capabilities.mcp_tools for the worker's own servers that the
// task needs, so that the list filters them as it filters a bridged server.
// The worker's servers are those of workerEnv, its environment.
func (d Deps) limitOwn(spec run.Spec, needs []string, tools map[string][]string, workerEnv []string) *run.MCP {
	var own []string
	for _, need := range withoutBridged(needs, spec.MCP) {
		if kind, name, err := caps.ParseNeed(need); err == nil && kind == caps.KindMCP {
			own = append(own, name)
		}
	}
	var codexArgs, allowed []string
	if c := d.Agents[spec.Route.Agent].Caps; c != nil {
		codexArgs, allowed = c.Limit(spec.Cwd, own, tools, workerEnv)
	}
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
