package paidruns

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/paidrules"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// Kills the mutation that hides a missing-window read behind a paid verdict,
// or lets a successful worker report admit a peer whose own read failed.
func TestPeerReadFailureAfterHealthyOverageReport(t *testing.T) {
	w := newGateWorld(t)
	ctx := context.Background()
	first := w.acquire(w.peerOpenRequest("codex", workerenv.Free, 801))
	if err := w.gate.CloseSession(ctx, first.Reservation.ID); err != nil {
		t.Fatal(err)
	}
	w.now = w.now.Add(5 * time.Minute)
	w.used, w.overage = 24, true
	report, err := w.gate.Report(ctx, w.request("codex", workerenv.Worker, workerenv.Free, 802).Input)
	if err != nil || report.Assessment.Verdict != paidrules.SubscriptionOverage {
		t.Fatalf("report: %+v, %v", report, err)
	}
	snapshot, ok := w.gate.Cache.Latest("codex")
	if !ok || len(snapshot.Windows) == 0 || snapshot.Windows[0].UsedPercent != 24 {
		t.Fatalf("report lost the available quota: %+v", snapshot)
	}
	read := w.gate.Cache.Read
	w.gate.Cache.Read = func(ctx context.Context, p workerenv.Process) (quota.Snapshot, error) {
		if p.Shape == workerenv.Peer {
			return quota.Snapshot{}, errors.New("no quota windows")
		}
		return read(ctx, p)
	}
	r := w.refusal(w.peerOpenRequest("codex", workerenv.Free, 803), ReasonFreeVariantPaid)
	if r.Verdict != paidrules.Paid || !strings.Contains(r.Error(), "quota read returned no supported windows") {
		t.Fatalf("missing actual read failure: %v", r)
	}
	if len(w.state().Settled) != 0 {
		t.Fatal("a refused read wrote run evidence")
	}
	w.gate.Cache.Read = read
	later := w.acquire(w.peerOpenRequest("codex", workerenv.Free, 804))
	if later.Assessment.Verdict != paidrules.SubscriptionOverage {
		t.Fatalf("later read: %s", later.Assessment.Verdict)
	}
}

// Kills the mutation that prints arbitrary reader errors (which can contain
// secrets), or describes a timeout as missing quota windows.
func TestReadFailureRefusalUsesSafeReasons(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"timeout", quota.ErrTimedOut, "quota read timed out or was canceled"},
		{"unknown", errors.New("PRIVATE credential and route"), "quota read failed; subscription billing could not be verified"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newGateWorld(t)
			w.gate.Cache.Read = func(context.Context, workerenv.Process) (quota.Snapshot, error) { return quota.Snapshot{}, tc.err }
			r := w.refusal(w.peerOpenRequest("codex", workerenv.Free, 805), ReasonFreeVariantPaid)
			if !strings.Contains(r.Error(), tc.want) || strings.Contains(r.Error(), "PRIVATE") {
				t.Fatalf("unsafe or missing reason: %v", r)
			}
		})
	}
}
