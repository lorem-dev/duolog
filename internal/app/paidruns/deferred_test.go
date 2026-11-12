package paidruns

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/paidrules"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

func TestDeferredSettlementSurvivesContentionAndReplaysOnce(t *testing.T) {
	clock := ledgerNow
	st := testStore(t, &clock)
	mustAdmit(t, st, paidTask("ended", "claude", 0.5))
	ctx := context.Background()
	unlock, err := st.lock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	measurement := paidrules.Settlement{ID: "ended", Agent: "claude", Input: 1234, Output: 56,
		Started: true, CompleteUsage: true, PricedUSD: 0.25, Ended: clock}
	// The account lock can be unavailable while the result is saved.
	if err := st.DeferSettlement(measurement); err != nil {
		t.Fatal(err)
	}
	unlock()
	path := filepath.Join(st.Dir, settlementFile(measurement.ID))
	// Windows has no Unix permission bits to check.
	if info, err := os.Stat(path); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("deferred settlement permissions: %v (%v)", info, err)
	}
	clock = clock.Add(time.Hour)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Go(func() {
			// A new Store models a later command after the supervisor ended.
			next := &Store{Dir: st.Dir, Now: st.Now}
			errs[i] = next.Reconcile(ctx, ReconcileInput{Process: func(paidrules.Owner) Presence { return Gone }})
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	s := readState(t, st)
	got := s.Settled[measurement.ID]
	if len(s.Reservations) != 0 || len(s.Settled) != 1 || got.ChargedUSD != 0.25 || got.Input != 1234 || !got.Ended.Equal(measurement.Ended) {
		t.Fatalf("measured settlement lost or charged twice: %+v", s)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("settled retry file remains: %v", err)
	}
}

func TestFinishQuotaReadCanTakeAccountLock(t *testing.T) {
	w := newGateWorld(t)
	w.cfg.Balance.MaxAgeSeconds = 10
	p := w.acquire(w.request("claude", "worker", "free", 100))
	w.now = w.now.Add(30 * time.Second)
	read := w.gate.Cache.Read
	var readErr error
	called := false
	w.gate.Cache.Read = func(ctx context.Context, process workerenv.Process) (quota.Snapshot, error) {
		called = true
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		readErr = w.store.View(ctx, func(State) error { return nil })
		return read(ctx, process)
	}
	w.finish(p, paidrules.Settlement{Started: true, CompleteUsage: true, Input: 1000})
	if !called || readErr != nil {
		t.Fatalf("quota read could not take account lock: called=%v err=%v", called, readErr)
	}
}
