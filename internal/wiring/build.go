// Package wiring builds the dependencies of the app packages from the real
// adapters and the kept packages.
package wiring

import (
	"context"
	"crypto/rand"
	"os"
	"os/exec"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/adapter/mcpauth"
	"github.com/lorem-dev/duolog/internal/adapter/paidstore"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/doctor"
	"github.com/lorem-dev/duolog/internal/app/lead"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/app/peer"
	"github.com/lorem-dev/duolog/internal/app/profile"
	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/rex"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// DelegateDeps fills every field of delegate.Deps with the real function.
func DelegateDeps() delegate.Deps {
	return delegate.Deps{
		Names:       Names(),
		Agents:      delegateAgents(),
		Lead:        Leads(),
		OAuth:       mcpauth.New(),
		RunLogin:    kit.RunLogin,
		Build:       workerenv.Build,
		DroppedKeys: workerenv.DroppedKeys,
		PaidGate:    paidGate,
		GateBinary:  workerenv.GateBinary,
		Canonical:   kit.Canonical,
	}
}

// Leads gives the Lead of each registered agent, keyed by agent name.
func Leads() map[string]lead.Lead {
	leads := map[string]lead.Lead{}
	for _, a := range Agents() {
		leads[a.Facts.Name] = a.Lead
	}
	return leads
}

// LeadDeps fills every field of lead.Deps with the real function.
func LeadDeps() lead.Deps {
	var facts []agent.Descriptor
	for _, a := range Agents() {
		facts = append(facts, a.Facts)
	}
	return lead.Deps{
		Agents:   Leads(),
		Facts:    facts,
		Registry: session.Open,
		PID:      session.AgentPID,
		Home:     os.UserHomeDir,
		Now:      time.Now,
	}
}

// AgentAncestor names the agent among this process's ancestors, from the
// process names of the registered agents.
func AgentAncestor() string {
	names, helpers, _ := agentProcessNames()
	return session.AgentAncestor(names, helpers)
}

// ParentAgent names the agent that is this process's direct parent, from the
// process names of the registered agents, and for node from their scripts.
func ParentAgent() string {
	return session.ParentAgent(agentProcessNames())
}

// ClaudeName is the agent name ParentAgent gives Claude Code.
func ClaudeName() string { return claude.Facts.Name }

// agentProcessNames are the binaries and the helper processes of the
// registered agents, and the scripts node runs them as, each naming its agent.
func agentProcessNames() (names, helpers []string, scripts map[string]string) {
	scripts = map[string]string{}
	for _, a := range Agents() {
		names = append(names, a.Facts.Binaries...)
		helpers = append(helpers, a.Facts.Helpers...)
		for _, s := range a.Facts.Scripts {
			scripts[s] = a.Facts.Name
		}
	}
	return names, helpers, scripts
}

// delegateRunner binds delegate.Run to its Deps.
func delegateRunner(d delegate.Deps) delegate.Runner {
	return func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		return delegate.Run(ctx, d, r)
	}
}

// systemClock is the real time.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// PeerDeps fills every field of peer.Deps with the real function. The account
// gates and the delegation are those of d.
func PeerDeps(d delegate.Deps) peer.Deps {
	hasQuota := map[string]bool{}
	for name, a := range d.Agents {
		hasQuota[name] = a.Quota != nil
	}
	return peer.Deps{
		Quota:     hasQuota,
		Clock:     systemClock{},
		Pauses:    peer.Pauses{Poll: 2 * time.Second, Settle: time.Second, Send: 700 * time.Millisecond, Key: 150 * time.Millisecond, WorkGrace: 10 * time.Second},
		Models:    peerModels(),
		Panes:     peerPanes(),
		Pane:      rex.Pane{},
		ResetTime: resetTime,
		Notify:    kit.Notify,
		RecordPeer: func(reg *session.Registry, e session.Entry, limit int) error {
			return reg.RecordLimited(e, limit)
		},
		OnEnvFile: func(string) {},
		LeadName:  func(agent string, pid int) string { return delegate.LeadName(d, agent, pid) },
		Delegate:  delegateRunner(d),
		PaidGate:  func(cfg *config.Config) (*paidruns.Gate, error) { return d.PaidGate(cfg) },
		HeldGate: func(agent, root string, env []string) error {
			return delegate.HeldGate(d, agent, root, env)
		},
		AdoptWorktree: func(agent, lead, root string, env []string) error {
			return delegate.AdoptWorktree(d, agent, lead, root, env, os.Stderr)
		},
		Build:         workerenv.Build,
		DroppedKeys:   workerenv.DroppedKeys,
		Executable:    os.Executable,
		StartDetached: kit.StartDetached,
	}
}

// ProfileDeps fills every field of profile.Deps with the real function.
func ProfileDeps(d delegate.Deps) profile.Deps {
	return profile.Deps{
		Delegate: delegateRunner(d),
		Rand: func() bool {
			var bit [1]byte
			if _, err := rand.Read(bit[:]); err != nil {
				panic(err)
			}
			return bit[0]&1 == 1
		},
		RunCheck: kit.RunCheck,
	}
}

// DoctorDeps fills every field of doctor.Deps with the real function.
func DoctorDeps(d delegate.Deps) doctor.Deps {
	probes := map[string]doctor.Probe{}
	for _, a := range Agents() {
		probes[a.Facts.Name] = a.Probe
	}
	return doctor.Deps{
		PaidGate:        func(cfg *config.Config) (*paidruns.Gate, error) { return d.PaidGate(cfg) },
		GateBinary:      workerenv.GateBinary,
		Build:           workerenv.Build,
		DroppedKeys:     workerenv.DroppedKeys,
		PaidClaudeRoute: func(name string) bool { return workerenv.RouteName(claude.Facts.Name, name, "") },
		Names:           Names(),
		Agents:          doctorAgents(),
		ClaudeFolder:    claude.ClaudeFolder,
		Probes:          probes,
		Available:       func(agent string) bool { _, err := exec.LookPath(agent); return err == nil },
		InstallCheck:    installCheck,
		LeadCompact:     leadCompact,
		StatusLineLine:  statusLineLine,
		ClaudePlugin:    claudePlugin,
		PluginMissing:   pluginMissing,
	}
}

// paidGate opens the account admission gate of one delegation: the user's
// account state, reads of the quota each built process sees, and the settings
// layers each build reads.
func paidGate(cfg *config.Config) (*paidruns.Gate, error) {
	st, err := paidruns.OpenUserState("")
	if err != nil {
		return nil, err
	}
	readers := QuotaDeps(func(agent, kind string, raw []byte) error {
		_, err := st.Identity(agent, kind, raw)
		return err
	})
	return &paidruns.Gate{Store: st, Cache: &paidstore.Cache{Read: readers.Read, Dir: st.Dir}, Config: cfg, Layers: paidruns.ReadLayers}, nil
}
