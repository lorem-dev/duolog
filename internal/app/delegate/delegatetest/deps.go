// Package delegatetest gives tests of delegate and of its callers fakes for
// every field of delegate.Deps: no worker starts, no model lookup or quota
// read starts a binary, and nothing touches the user's account state. Only
// tests import it.
package delegatetest

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/workertest"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	leadio "github.com/lorem-dev/duolog/internal/app/lead"
	"github.com/lorem-dev/duolog/internal/app/lead/leadtest"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/app/paidruns/paidrunstest"
	"github.com/lorem-dev/duolog/internal/capability"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/run"
)

// Fake holds the fakes of one test and what they were called with.
type Fake struct {
	mu sync.Mutex
	// Calls are the specs the fake workers received, in order.
	Calls []run.Spec
	// Gate admits the runs, with fake quota reads in its own account state.
	Gate *paidruns.Gate
	// Result is what the fake workers return for every run.
	Result run.Result
}

// New returns the fakes for t. Its gate keeps its account state under the
// test's temporary folder.
func New(t *testing.T) *Fake {
	t.Helper()
	f := &Fake{Result: run.Result{Done: true, Summary: "Completed.", Needs: []string{}, SessionID: "worker-id"}}
	var err error
	if f.Gate, err = paidrunstest.Gate(filepath.Join(t.TempDir(), "accounts"), nil); err != nil {
		t.Fatal(err)
	}
	return f
}

// Deps is New(t).Deps(): fakes for every field.
func Deps(t *testing.T) delegate.Deps {
	t.Helper()
	return New(t).Deps()
}

// Deps returns the fakes as delegate.Deps.
func (f *Fake) Deps() delegate.Deps {
	return delegate.Deps{
		// The fake workers start nothing but give the launch words of the real
		// agents, so the process the gate admits is the one a real run builds.
		Agents: map[string]delegate.Agent{
			"claude": {Worker: &Worker{f: f, launch: workertest.For("claude").Launch}, Models: models{}, Login: Login{Agent: "claude"}},
			"codex":  {Worker: &Worker{f: f, launch: workertest.For("codex").Launch}, Models: models{}, Login: Login{Agent: "codex"}},
		},
		RunLogin: declineLogins,
		Lead:     map[string]leadio.Lead{"claude": &leadtest.Fake{}, "codex": &leadtest.Fake{}},
		ScanCapabilities: func(_ context.Context, agent, _ string, _ []string) (capability.Inventory, error) {
			return capability.Inventory{Agent: agent, Unknown: true}, nil
		},
		BridgeMCP: func(_, _, _ string, _ []string, _ map[string][]string, _ []string) (*run.MCP, map[string]string) {
			return nil, nil
		},
		PaidGate: func(cfg *config.Config) (*paidruns.Gate, error) {
			g := *f.Gate
			g.Config = cfg
			return &g, nil
		},
		GateBinary: func(string) (string, error) { return filepath.Join(string(filepath.Separator), "duolog"), nil },
		Canonical:  filepath.Clean,
	}
}

// models resolves every target to its own model name, with no catalog.
type models struct{}

func (models) Resolve(_ context.Context, _ *config.Config, _ string, t config.Target) (string, error) {
	return t.Model, nil
}
