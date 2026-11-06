package delegatetest

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/launch/launchtest"
	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/launch"
)

// TrustPort is delegate.Trust under a name that an adapter's test can use
// without importing delegate, which an agent adapter may not.
type TrustPort = launchtest.Trust

// TrustCall is one call of a fake Trust.
type TrustCall struct {
	Method, Root string
	Environ      []string
}

// Trust is a delegate.Trust that records each call and returns scripted
// results. Like the adapters it fails with agent.ErrNoHome for an
// environment that names no home folder.
type Trust struct {
	// State is what Hooks returns, FolderTrusted what Folder returns.
	State         agent.TrustState
	FolderTrusted bool
	// Problem and Held are what GateProblem and HeldGateProblem return.
	Problem, Held string

	mu    sync.Mutex
	calls []TrustCall
}

// Calls are the calls so far, in order.
func (f *Trust) Calls() []TrustCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]TrustCall(nil), f.calls...)
}

func (f *Trust) record(method, root string, environ []string) {
	f.mu.Lock()
	f.calls = append(f.calls, TrustCall{method, root, environ})
	f.mu.Unlock()
}

func noHome(environ []string) error {
	name := "HOME"
	if runtime.GOOS == "windows" {
		name = "USERPROFILE"
	}
	if launch.Getenv(environ, name) == "" {
		return fmt.Errorf("%w (%s)", agent.ErrNoHome, name)
	}
	return nil
}

// Hooks returns the scripted state.
func (f *Trust) Hooks(root string, environ []string) (agent.TrustState, error) {
	f.record("Hooks", root, environ)
	if err := noHome(environ); err != nil {
		return agent.TrustState{}, err
	}
	return f.State, nil
}

// Folder returns the scripted folder trust.
func (f *Trust) Folder(root string, environ []string) (bool, error) {
	f.record("Folder", root, environ)
	if err := noHome(environ); err != nil {
		return false, err
	}
	return f.FolderTrusted, nil
}

// GateProblem returns the scripted problem.
func (f *Trust) GateProblem(agent.TrustState, bool) string { return f.Problem }

// HeldGateProblem returns the scripted held-gate problem.
func (f *Trust) HeldGateProblem(string, []string, string) string { return f.Held }

// TrustContract runs the cases that every delegate.Trust must pass, the fake
// and each agent's adapter: no hooks file gives Installed false, and an
// environment without a home folder gives agent.ErrNoHome. What only one
// agent has, such as an approved gate command that differs from the one
// duolog installs, is that adapter's own test.
func TrustContract(t *testing.T, newTrust func(t *testing.T) TrustPort) {
	t.Helper()
	t.Run("no hooks file", func(t *testing.T) {
		home, root := t.TempDir(), t.TempDir()
		environ := []string{"HOME=" + home, "USERPROFILE=" + home}
		state, err := newTrust(t).Hooks(root, environ)
		if err != nil || state.Installed {
			t.Fatalf("Hooks = %+v, %v; want no gate and no error", state, err)
		}
	})
	t.Run("missing home", func(t *testing.T) {
		none := []string{"PATH=" + t.TempDir()}
		if _, err := newTrust(t).Hooks(t.TempDir(), none); !errors.Is(err, agent.ErrNoHome) {
			t.Fatalf("Hooks without a home: %v, want agent.ErrNoHome", err)
		}
	})
}
