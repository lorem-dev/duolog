package delegate

import (
	"os"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/caps"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	corelead "github.com/lorem-dev/duolog/internal/core/lead"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/run"
)

// workerSpec is the run spec of the worker of route: the admitted process
// already holds the resolved native flags and the Codex sandbox pins, and the
// runner refuses it if this spec asks for other ones.
func workerSpec(d Deps, cfg *config.Config, opts Options, route coreroute.Route, wm workerMode, dir, root string, stdin []byte, bridged *run.MCP, env []string, name string) run.Spec {
	spec := run.Spec{SessionName: name, Route: route, Mode: wm.Level, Native: wm.Native, Network: wm.Network, WritableRoots: wm.WritableRoots, TaskDir: dir, Cwd: root, Prompt: caps.NeedsLine, Stdin: stdin, Persistent: route.Session == "resume", KeepMCP: needsKind(withoutBridged(opts.Needs, bridged), caps.KindMCP, caps.KindPlugin), KeepSkills: needsKind(opts.Needs, caps.KindSkill, caps.KindPlugin), MCP: bridged, ProtectedRoots: opts.ProtectedRoots}
	spec.MCP = d.limitOwn(spec, opts.Needs, cfg.Capabilities.MCPTools, env)
	spec.MaxTurns = opts.MaxTurns
	return spec
}

// LeadName reads the display name that the lead's agent keeps for the session
// of process pid; "" when the agent keeps none or it cannot be read. Naming a
// worker never stops it from starting.
func LeadName(d Deps, agent string, pid int) string {
	if l := d.Lead[agent]; l != nil {
		return l.Name(os.Environ(), pid)
	}
	return ""
}

// workerName is the name a worker starts under, with the display name of
// lead's session or, without one, the start of its id.
func workerName(d Deps, role string, lead session.Lead) string {
	return corelead.SessionLabel("worker", role, LeadName(d, lead.Agent, lead.PID), lead.ID)
}

// admittedInput adds to in the whole launch that the account gate admits:
// Codex's exec with its sandbox flag and pins, Claude's -p with its permission
// flag. The runner adds none after it.
func admittedInput(d Deps, accounts *paidruns.Gate, in corelaunch.ProcessInput, route coreroute.Route, wm workerMode) corelaunch.ProcessInput {
	in.Launch = d.Agents[route.Agent].Worker.Launch(run.Spec{Route: route, Mode: wm.Level, Native: wm.Native, Network: wm.Network, WritableRoots: wm.WritableRoots})
	in.ProviderEnvKey = accounts.Cache.ProviderEnvKey(route.Agent)
	return in
}
