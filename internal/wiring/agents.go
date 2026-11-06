// Package wiring is the one place that knows every agent.
package wiring

import (
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/doctor"
	"github.com/lorem-dev/duolog/internal/app/hook"
	"github.com/lorem-dev/duolog/internal/app/install"
	"github.com/lorem-dev/duolog/internal/app/lead"
	"github.com/lorem-dev/duolog/internal/app/peer"
	"github.com/lorem-dev/duolog/internal/app/remember"
	agentsync "github.com/lorem-dev/duolog/internal/app/sync"
	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/agentfile"
)

// doctorAgents gives doctor the ports of each registered agent by name.
func doctorAgents() map[string]doctor.Agent {
	out := map[string]doctor.Agent{}
	for _, a := range Agents() {
		out[a.Facts.Name] = doctor.Agent{Trust: a.Trust, Installer: a.Installer}
	}
	return out
}

// Agent is one registered agent. Later ports add their fields here.
type Agent struct {
	Facts  agent.Descriptor
	Lead   lead.Lead
	Events hook.Events
	// Worker runs this agent's workers.
	Worker delegate.Worker
	// ResetTime parses the time a limit message of this agent says the quota
	// returns, or gives a fallback; never the zero time.
	ResetTime func(text string, now time.Time) time.Time
	// Models resolves this agent's model names.
	Models delegate.ModelResolver
	// Login checks this agent's logins and gives its login commands.
	Login delegate.Login
	// Trust reads the agent's hook and folder trust. The app packages that
	// need less of it declare smaller interfaces that this one satisfies.
	Trust delegate.Trust
	// Installer writes the agent's hooks and skill. An agent whose Installer
	// also implements install.Plugins has a plugin that registers the same.
	Installer install.Installer
	// Sync reads and writes the agent's files for duolog sync; nil means the
	// agent takes no part in it.
	Sync agentfile.Format
	// Allow writes the agent's own allow rules for `duolog remember --native`;
	// nil means the agent has none.
	Allow remember.NativeAllow
	// Probe is how doctor checks the agent.
	Probe doctor.Probe
	// Tree guards a calibration tree for the agent's worker. It has the
	// methods of calibration.TreeGuard, which wiring cannot import.
	Tree TreeGuard
	// Pane opens the agent in a peer pane; nil means `peer open` refuses the
	// agent with "peer open: <agent> has no pane support". The texts of its
	// pane are Facts.Pane.
	Pane peer.PaneAgent
}

// TreeGuard is what an agent needs of a calibration tree: see
// calibration.TreeGuard, whose method set it repeats.
type TreeGuard interface {
	Prepare(dir string, environ []string) error
	Check(dir string, environ []string) error
}

// Agents returns the registered agents in order: claude, then codex.
func Agents() []Agent {
	return []Agent{
		{Facts: claude.Facts, Lead: claude.Lead{}, Events: claude.Events{}, Worker: claude.Worker{}, ResetTime: claude.ResetTime, Models: claude.Resolver{}, Login: claude.Login{}, Trust: claude.Trust{}, Installer: claude.Installer{}, Sync: claude.Format{}, Allow: claude.NativeAllow{}, Probe: claude.Probe{}, Tree: claude.Tree{}, Pane: claude.Pane{}},
		{Facts: codex.Facts, Lead: codex.Lead{}, Events: codex.Events{}, Worker: codex.Worker{}, ResetTime: codex.ResetTime, Models: codex.Resolver{}, Login: codex.Login{}, Trust: codex.Trust{}, Installer: codex.Installer{}, Sync: codex.Format{}, Allow: codex.NativeAllow{}, Probe: codex.Probe{}, Tree: codex.Tree{}, Pane: codex.Pane{}},
	}
}

// Claude's Installer is also its Plugins, and Codex's its Approvals: install
// asserts them.
var (
	_ install.Plugins   = claude.Installer{}
	_ install.Approvals = codex.Installer{}
)

// InstallDeps gives install the Installer of each registered agent, in order.
func InstallDeps() install.Deps {
	var d install.Deps
	for _, a := range Agents() {
		d.Agents = append(d.Agents, install.Agent{Name: a.Facts.Name, Installer: a.Installer})
	}
	return d
}

// HookEnv fills the Events and Lead of env.Agent. An agent that is not
// registered gets none, and every hook of it does nothing.
func HookEnv(env hook.Env) hook.Env {
	if a, ok := Find(env.Agent); ok {
		env.Events, env.Lead = a.Events, a.Lead
	}
	return env
}

// Find returns the registered agent called name.
func Find(name string) (Agent, bool) {
	for _, a := range Agents() {
		if a.Facts.Name == name {
			return a, true
		}
	}
	return Agent{}, false
}

// Names returns the names of the registered agents, in order.
func Names() []string {
	all := Agents()
	names := make([]string, len(all))
	for i, a := range all {
		names[i] = a.Facts.Name
	}
	return names
}

// NativeAgents returns the registered agents in order, as `remember --native`
// writes for them.
func NativeAgents() []remember.Agent {
	all := Agents()
	agents := make([]remember.Agent, len(all))
	for i, a := range all {
		agents[i] = remember.Agent{Name: a.Facts.Name, Allow: a.Allow}
	}
	return agents
}

// SyncFormats returns the sync format of each registered agent that has one.
func SyncFormats() agentsync.Formats {
	formats := agentsync.Formats{}
	for _, a := range Agents() {
		if a.Sync != nil {
			formats[a.Facts.Name] = a.Sync
		}
	}
	return formats
}

// delegateAgents builds the ports of delegate.Deps.Agents from the registry.
func delegateAgents() map[string]delegate.Agent {
	out := map[string]delegate.Agent{}
	for _, a := range Agents() {
		out[a.Facts.Name] = delegate.Agent{Worker: a.Worker, Models: a.Models, Login: a.Login, Trust: a.Trust}
	}
	return out
}

// peerModels builds the model resolvers of peer.Deps from the registry.
func peerModels() map[string]peer.ModelResolver {
	out := map[string]peer.ModelResolver{}
	for _, a := range Agents() {
		out[a.Facts.Name] = a.Models
	}
	return out
}

// peerPanes builds the pane support of peer.Deps from the registry. An agent
// with no Pane keeps its texts and has a nil Agent.
func peerPanes() map[string]peer.Pane {
	out := map[string]peer.Pane{}
	for _, a := range Agents() {
		out[a.Facts.Name] = peer.Pane{Agent: a.Pane, Facts: a.Facts.Pane}
	}
	return out
}

// resetTime parses a limit message of the named agent; an unknown agent gets
// no parse.
func resetTime(name, text string, now time.Time) time.Time {
	for _, a := range Agents() {
		if a.Facts.Name == name {
			return a.ResetTime(text, now)
		}
	}
	return now.Add(time.Hour)
}
