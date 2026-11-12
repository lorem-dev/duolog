package doctor

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/app/paidruns/paidrunstest"
	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/route"
)

func shortCheckBudget(t *testing.T) {
	t.Helper()
	old := checkTimeout
	checkTimeout = 200 * time.Millisecond
	t.Cleanup(func() { checkTimeout = old })
}

// timedProbe keeps the normal fake's model and answers, and intercepts the
// operations whose contexts these tests exercise.
type timedProbe struct {
	base     Probe
	metadata func(context.Context, ...string) ([]byte, error)
	call     func(context.Context) error
}

func (p timedProbe) Model(cfg *config.Config, visible []route.Entry) (string, error) {
	return p.base.Model(cfg, visible)
}

func (p timedProbe) Metadata(ctx context.Context, args ...string) ([]byte, error) {
	return p.metadata(ctx, args...)
}

func (p timedProbe) Probe(ctx context.Context, cfg *config.Config, in corelaunch.Process, dir, model string, started func(int) error) (Usage, error) {
	if p.call != nil {
		if err := p.call(ctx); err != nil {
			return Usage{}, err
		}
	}
	return p.base.Probe(ctx, cfg, in, dir, model, started)
}

func liveCheck(t *testing.T, ctx context.Context) {
	t.Helper()
	at, ok := ctx.Deadline()
	if !ok || ctx.Err() != nil || time.Until(at) > checkTimeout {
		t.Fatalf("check has no fresh bounded context: deadline %v, err %v", at, ctx.Err())
	}
}

func TestDoctorBusyAccountIsAWarningAndCatalogStillRuns(t *testing.T) {
	cfg, opts, a := setup(t)
	shortCheckBudget(t)
	reads := 0
	g := reportGate(t, cfg, func(ctx context.Context, p corelaunch.Process) (account.Snapshot, error) {
		reads++
		return paidrunstest.Subscription(ctx, p)
	})
	// A separate flock handle represents another duolog process. Keep it
	// held throughout doctor, rather than freeing it when a timer expires.
	lock := flock.New(filepath.Join(g.Store.Dir, "accounts.lock"))
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	catalog := false
	testDeps.Probes["codex"] = timedProbe{base: a.codex, metadata: func(ctx context.Context, args ...string) ([]byte, error) {
		liveCheck(t, ctx)
		catalog = catalog || args[0] == "debug"
		return a.codex.Metadata(ctx, args...)
	}}
	got := Run(context.Background(), testDeps, cfg, opts)
	requireFinding(t, got, "another duolog process holds the account state", "not an account fault")
	for _, f := range got {
		if strings.Contains(f.Message, "account state") && f.Level != "warning" {
			t.Fatalf("busy state is a fault: %+v", f)
		}
	}
	if !catalog || reads != 0 || strings.Contains(joined(got), "context deadline exceeded") {
		t.Fatalf("catalog %t, quota reads %d, findings:\n%s", catalog, reads, joined(got))
	}
}

func TestDoctorChecksAfterSlowVersionHaveTheirOwnBudget(t *testing.T) {
	cfg, opts, a := setup(t)
	shortCheckBudget(t)
	reportGate(t, cfg, func(ctx context.Context, p corelaunch.Process) (account.Snapshot, error) {
		liveCheck(t, ctx)
		return paidrunstest.Subscription(ctx, p)
	})
	testDeps.Probes["claude"] = timedProbe{base: a.claude, metadata: func(ctx context.Context, _ ...string) ([]byte, error) {
		liveCheck(t, ctx)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	catalog := false
	testDeps.Probes["codex"] = timedProbe{base: a.codex, metadata: func(ctx context.Context, args ...string) ([]byte, error) {
		liveCheck(t, ctx)
		catalog = catalog || args[0] == "debug"
		return a.codex.Metadata(ctx, args...)
	}}
	got := Run(context.Background(), testDeps, cfg, opts)
	requireFinding(t, got, "claude version: context deadline exceeded")
	requireFinding(t, got, "codex version 0.165.0")
	requireFinding(t, got, "codex worker: subscription")
	if !catalog || strings.Contains(joined(got), "Codex catalog:") {
		t.Fatal(joined(got))
	}
}

func TestDoctorBillingAfterSlowReadHasItsOwnBudget(t *testing.T) {
	cfg, opts, _ := setup(t)
	shortCheckBudget(t)
	reads := 0
	reportGate(t, cfg, func(ctx context.Context, p corelaunch.Process) (account.Snapshot, error) {
		liveCheck(t, ctx)
		reads++
		if reads == 1 {
			<-ctx.Done()
			return account.Snapshot{}, ctx.Err()
		}
		return paidrunstest.Subscription(ctx, p)
	})
	got := Run(context.Background(), testDeps, cfg, opts)
	requireFinding(t, got, "claude peer: subscription")
	requireFinding(t, got, "codex worker: subscription")
	if strings.Contains(joined(got), "Codex catalog:") {
		t.Fatal(joined(got))
	}
}

func TestDoctorProbesHaveSeparateBudgets(t *testing.T) {
	cfg, opts, a := setup(t)
	shortCheckBudget(t)
	reportGate(t, cfg, nil)
	opts.Probe = true
	testDeps.Probes["claude"] = timedProbe{base: a.claude, metadata: a.claude.Metadata, call: func(ctx context.Context) error {
		liveCheck(t, ctx)
		<-ctx.Done()
		return ctx.Err()
	}}
	testDeps.Probes["codex"] = timedProbe{base: a.codex, metadata: a.codex.Metadata, call: func(ctx context.Context) error {
		liveCheck(t, ctx)
		return nil
	}}
	got := Run(context.Background(), testDeps, cfg, opts)
	requireFinding(t, got, "claude probe failed: context deadline exceeded")
	requireFinding(t, got, "codex probe succeeded")
}

func TestDoctorPreservesCallerCancellation(t *testing.T) {
	cfg, opts, a := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	testDeps.Probes["claude"] = timedProbe{base: a.claude, metadata: func(context.Context, ...string) ([]byte, error) {
		cancel()
		return nil, ctx.Err()
	}}
	testDeps.Probes["codex"] = timedProbe{base: a.codex, metadata: func(ctx context.Context, _ ...string) ([]byte, error) {
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatal("doctor detached from caller cancellation")
		}
		return nil, ctx.Err()
	}}
	Run(ctx, testDeps, cfg, opts)
}

func TestDoctorBusyIdentityDoesNotReportAnAccountVerdict(t *testing.T) {
	cfg, opts, _ := setup(t)
	reportGate(t, cfg, func(context.Context, corelaunch.Process) (account.Snapshot, error) {
		return account.Snapshot{}, account.ErrStateBusy
	})
	got := Run(context.Background(), testDeps, cfg, opts)
	requireFinding(t, got, "claude worker billing:", "not an account fault")
	if strings.Contains(joined(got), "read:failed") {
		t.Fatal(joined(got))
	}
}

func TestBillingReadDoesNotHoldAccountLock(t *testing.T) {
	cfg, opts, _ := setup(t)
	var g *paidruns.Gate
	g = reportGate(t, cfg, func(ctx context.Context, p corelaunch.Process) (account.Snapshot, error) {
		lock := flock.New(filepath.Join(g.Store.Dir, "accounts.lock"))
		defer func() { _ = lock.Close() }()
		ok, err := lock.TryLock()
		if err != nil || !ok {
			t.Fatalf("quota read holds account lock: %v", err)
		}
		return paidrunstest.Subscription(ctx, p)
	})
	Run(context.Background(), testDeps, cfg, opts)
}
