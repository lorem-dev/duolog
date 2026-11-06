package doctortest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/usage"
)

// Probe has the methods of doctor.Probe. This package does not import doctor,
// so the tests of doctor itself and of an adapter, which may not import it,
// can use the fake and the contract.
type Probe interface {
	Model(cfg *config.Config, visible []route.Entry) (string, error)
	Probe(ctx context.Context, cfg *config.Config, p corelaunch.Process, dir, model string, started func(pid int) error) (usage.Probed, error)
	Metadata(ctx context.Context, args ...string) ([]byte, error)
}

// ContractVersion is what every probe of the contract answers to --version.
const ContractVersion = "9.9.9"

// contractUsage is what every probe of the contract measures: 100 input
// tokens, 20 of them cached, and 5 output tokens.
var contractUsage = usage.Probed{Tokens: usage.Tokens{Input: 100, Cached: 20, Output: 5}, Known: true}

// ContractUsage returns what a probe of the contract measures.
func ContractUsage() usage.Probed { return contractUsage }

// usageParses checks that Probe reports ContractUsage and tells started the
// pid of the process once.
func usageParses(t *testing.T, probe Probe, p corelaunch.Process) {
	t.Helper()
	pids := 0
	got, err := probe.Probe(context.Background(), nil, p, t.TempDir(), "fake-model", func(pid int) error {
		if pid <= 0 {
			t.Errorf("started got pid %d", pid)
		}
		pids++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Input != contractUsage.Input || got.Cached != contractUsage.Cached || got.Output != contractUsage.Output || !got.Known {
		t.Fatalf("Probe usage = %+v, want %+v", got, contractUsage)
	}
	if pids != 1 {
		t.Fatalf("started called %d times, want once", pids)
	}
}

// ProbeContract runs the cases every doctor.Probe meets. newProbe returns the
// probe and the process to run it as: with present true the agent's binary
// answers "9.9.9 (fake)" to --version and replies "ok" to the probe call with
// ContractUsage; with present false the binary is missing.
func ProbeContract(t *testing.T, newProbe func(t *testing.T, present bool) (Probe, corelaunch.Process)) {
	t.Helper()
	ctx := context.Background()
	t.Run("version", func(t *testing.T) {
		probe, _ := newProbe(t, true)
		out, err := probe.Metadata(ctx, "--version")
		if err != nil || !strings.Contains(string(out), ContractVersion) {
			t.Fatalf("Metadata(--version) = %q, %v, want the version line", out, err)
		}
	})
	t.Run("usage parses", func(t *testing.T) {
		probe, p := newProbe(t, true)
		usageParses(t, probe, p)
	})
	t.Run("started error stops the call", func(t *testing.T) {
		probe, p := newProbe(t, true)
		stop := errors.New("cannot record")
		if _, err := probe.Probe(ctx, nil, p, t.TempDir(), "fake-model", func(int) error { return stop }); !errors.Is(err, stop) {
			t.Fatalf("Probe = %v, want the error of started", err)
		}
	})
	t.Run("missing binary", func(t *testing.T) {
		probe, p := newProbe(t, false)
		if _, err := probe.Metadata(ctx, "--version"); err == nil {
			t.Fatal("Metadata of a missing binary: no error")
		}
		called := false
		if _, err := probe.Probe(ctx, nil, p, t.TempDir(), "fake-model", func(int) error { called = true; return nil }); err == nil {
			t.Fatal("Probe of a missing binary: no error")
		}
		if called {
			t.Fatal("started called for a process that never started")
		}
	})
}
