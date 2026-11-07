package delegatetest

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/core/caps"
	"github.com/lorem-dev/duolog/internal/core/run"
)

// CapsPort is delegate.Capabilities under a name that an adapter's test can
// use without importing delegate, which an agent adapter may not.
type CapsPort = delegate.Capabilities

// CapsCall is one call of a fake Caps.
type CapsCall struct {
	Method, Root string
	Environ      []string
	// Names are the servers Import was given, sorted.
	Names []string
}

// Caps is a delegate.Capabilities that records each call and returns scripted
// results. Like the adapters it refuses a server with its own reason, one of
// its own agent, and one that sets a variable duolog keeps for itself.
type Caps struct {
	// Agent is the agent the fake stands for.
	Agent string
	// Inventory is what Scan returns; with no Agent set, Scan gives an
	// unknown inventory of Agent.
	Inventory caps.Inventory
	// Defs is what Servers returns.
	Defs map[string]caps.Server
	// Why holds a refusal by server name, after the reasons every agent has.
	Why map[string]string

	mu    sync.Mutex
	calls []CapsCall
}

// Calls are the calls so far, in order.
func (f *Caps) Calls() []CapsCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]CapsCall(nil), f.calls...)
}

func (f *Caps) record(c CapsCall) {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
}

// Scan returns the scripted inventory.
func (f *Caps) Scan(_ context.Context, root string, environ []string) (caps.Inventory, error) {
	f.record(CapsCall{Method: "Scan", Root: root, Environ: environ})
	if f.Inventory.Agent == "" {
		return caps.Inventory{Agent: f.Agent, Unknown: true}, nil
	}
	return f.Inventory, nil
}

// Servers returns the scripted servers.
func (f *Caps) Servers(root string, environ []string) map[string]caps.Server {
	f.record(CapsCall{Method: "Servers", Root: root, Environ: environ})
	return f.Defs
}

// Refusal gives the server's own reason, the same-agent reason, or the
// scripted one.
func (f *Caps) Refusal(root string, s caps.Server, worker []string) string {
	f.record(CapsCall{Method: "Refusal", Root: root, Environ: worker, Names: []string{s.Name}})
	return f.refusal(s)
}

func (f *Caps) refusal(s caps.Server) string {
	if s.Why != "" {
		return s.Why
	}
	if s.Agent == f.Agent {
		return "the worker has the same agent"
	}
	return f.Why[s.Name]
}

// Import bridges every server it does not refuse, with no argument and no
// value: the names only.
func (f *Caps) Import(root string, servers map[string]caps.Server, _ map[string][]string, worker []string) (*run.MCP, map[string]string) {
	names := kit.SortedNames(servers)
	f.record(CapsCall{Method: "Import", Root: root, Environ: worker, Names: names})
	failed := map[string]string{}
	out := &run.MCP{}
	for _, name := range names {
		s := servers[name]
		if why := f.refusal(s); why != "" {
			failed[name] = why
			continue
		}
		if why := reserved(s); why != "" {
			failed[name] = why
			continue
		}
		out.Names = append(out.Names, name)
	}
	if len(out.Names) == 0 {
		return nil, failed
	}
	return out, failed
}

// reserved is the refusal of a server that sets a variable duolog keeps for
// itself, as every agent refuses it.
func reserved(s caps.Server) string {
	for _, p := range s.Env {
		if err := kit.ReservedName(p.Key); err != nil {
			return err.Error()
		}
	}
	return ""
}

// Limit limits nothing.
func (f *Caps) Limit(root string, names []string, _ map[string][]string, worker []string) (codexArgs, allowedTools []string) {
	f.record(CapsCall{Method: "Limit", Root: root, Environ: worker, Names: slices.Sorted(slices.Values(names))})
	return nil, nil
}

// CapabilitiesContract runs the cases that every delegate.Capabilities of
// agent must pass, the fake and each agent's adapter: a server's own reason
// and the same agent are refused word for word, by Refusal and by Import; a
// server that sets a variable duolog keeps for itself fails; a server that
// can go is bridged under its name; and an environment without a home folder
// defines no server. The reasons only one agent gives, such as a Codex -c key
// that cannot hold a name or Claude's ${ expansion, are that adapter's own
// cases.
func CapabilitiesContract(t *testing.T, agent string, newCaps func(t *testing.T) CapsPort) {
	t.Helper()
	other := "codex"
	if agent == "codex" {
		other = "claude"
	}
	t.Run("refusal reasons word for word", func(t *testing.T) {
		refusalsWordForWord(t, newCaps(t), agent, other)
	})
	t.Run("a server that would replace a worker variable fails", func(t *testing.T) {
		s := caps.Server{Name: "svc", Agent: other, Transport: "stdio", Command: "x", Env: []caps.Pair{{Key: "DUOLOG_DEPTH", Value: "0"}}}
		m, failed := newCaps(t).Import(t.TempDir(), map[string]caps.Server{"svc": s}, nil, append(worker(t), "DUOLOG_DEPTH=1"))
		if m != nil || !strings.Contains(failed["svc"], "a name duolog keeps for itself") {
			t.Fatalf("Import = %+v, %q; want refused as duolog's own name", m, failed)
		}
	})
	t.Run("a server that can go is bridged under its name", func(t *testing.T) {
		s := caps.Server{Name: "svc", Agent: other, Transport: "stdio", Command: "docs-server"}
		root, env := t.TempDir(), worker(t)
		c := newCaps(t)
		if why := c.Refusal(root, s, env); why != "" {
			t.Fatalf("Refusal = %q, want none", why)
		}
		m, failed := c.Import(root, map[string]caps.Server{"svc": s}, nil, env)
		if m == nil || len(failed) != 0 || !slices.Equal(m.Names, []string{"svc"}) {
			t.Fatalf("Import = %+v, %q; want svc bridged", m, failed)
		}
	})
	t.Run("missing home defines no server", func(t *testing.T) {
		if got := newCaps(t).Servers(t.TempDir(), []string{"PATH=" + t.TempDir()}); len(got) != 0 {
			t.Fatalf("Servers without a home = %v, want none", got)
		}
	})
}

// worker is the environment of a worker with an empty home folder.
func worker(t *testing.T) []string {
	home := t.TempDir()
	return []string{"HOME=" + home, "USERPROFILE=" + home, "PATH=" + t.TempDir()}
}

// refusalsWordForWord checks that c refuses a server with its own reason and
// one of agent, c's own agent, with the same-agent reason, word for word, by
// Refusal and by Import.
func refusalsWordForWord(t *testing.T, c CapsPort, agent, other string) {
	t.Helper()
	refused := map[string]caps.Server{
		"own":  {Name: "own", Agent: other, Transport: "stdio", Command: "x", Why: "it signs in with OAuth"},
		"same": {Name: "same", Agent: agent, Transport: "stdio", Command: "x"},
	}
	want := map[string]string{"own": "it signs in with OAuth", "same": "the worker has the same agent"}
	root, env := t.TempDir(), worker(t)
	for name, s := range refused {
		if got := c.Refusal(root, s, env); got != want[name] {
			t.Errorf("Refusal(%s) = %q, want %q", name, got, want[name])
		}
	}
	m, failed := c.Import(root, refused, nil, env)
	if m != nil || !maps.Equal(failed, want) {
		t.Fatalf("Import = %+v, %q; want nothing bridged and %q", m, failed, want)
	}
}
