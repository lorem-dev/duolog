// Package delegatetest gives tests of delegate and of its callers fakes for
// every field of delegate.Deps: no worker starts, no model lookup or quota
// read starts a binary, and nothing touches the user's account state. Only
// tests import it.
package delegatetest

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/app/paidruns/paidrunstest"
	"github.com/lorem-dev/duolog/internal/auth"
	"github.com/lorem-dev/duolog/internal/capability"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/lead"
	"github.com/lorem-dev/duolog/internal/runner"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// Fake holds the fakes of one test and what they were called with.
type Fake struct {
	mu sync.Mutex
	// Calls are the specs RunWorker received, in order.
	Calls []runner.Spec
	// Gate admits the runs, with fake quota reads in its own account state.
	Gate *paidruns.Gate
	// Result is what RunWorker returns for every run.
	Result runner.Result
}

// New returns the fakes for t. Its gate keeps its account state under the
// test's temporary folder.
func New(t *testing.T) *Fake {
	t.Helper()
	f := &Fake{Result: runner.Result{Done: true, Summary: "Completed.", Needs: []string{}, SessionID: "worker-id"}}
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
		RunWorker: func(_ context.Context, s runner.Spec) (runner.Result, error) {
			f.mu.Lock()
			f.Calls = append(f.Calls, s)
			f.mu.Unlock()
			if s.Started != nil {
				if err := s.Started(os.Getpid()); err != nil {
					return runner.Result{}, err
				}
			}
			return f.Result, nil
		},
		ResolveModel: func(_ context.Context, _ *config.Config, _ string, t config.Target) (string, error) {
			return t.Model, nil
		},
		ReadLeadState: func(string, string) (lead.LeadState, error) { return lead.LeadState{}, nil },
		ScanCapabilities: func(_ context.Context, agent, _ string, _ []string) (capability.Inventory, error) {
			return capability.Inventory{Agent: agent, Unknown: true}, nil
		},
		BridgeMCP: func(_, _, _ string, _ []string, _ map[string][]string, _ []string) (*runner.MCP, map[string]string) {
			return nil, nil
		},
		NewChecker: func(string, workerenv.Process) auth.Checker { return checker{} },
		PaidGate: func(cfg *config.Config) (*paidruns.Gate, error) {
			g := *f.Gate
			g.Config = cfg
			return &g, nil
		},
		GateBinary: func(string) (string, error) { return filepath.Join(string(filepath.Separator), "duolog"), nil },
		Canonical:  filepath.Clean,
	}
}

// checker passes every login.
type checker struct{}

func (checker) Agent(context.Context, string) *auth.Problem { return nil }
func (checker) Server(context.Context, string, string, *capability.Server) *auth.Problem {
	return nil
}
func (checker) Login(context.Context, auth.Problem, time.Duration) auth.LoginResult {
	return auth.Declined
}
func (checker) Forget(auth.Problem) {}
