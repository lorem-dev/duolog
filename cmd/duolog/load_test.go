package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/testbin"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// A hook process has no read in its cache: the line takes the limit of the
// tier admission recorded last, and the five-hour use the latest admission
// measured, rather than a small tier and no window.
func TestLeadLoadUsesAdmissionsTierAndWindow(t *testing.T) {
	clearModelMarkers(t)
	root, _ := peerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	recordListPeer(t, root)
	d := testDeps(t)
	open := d.Delegate.PaidGate
	d.Delegate.PaidGate = func(cfg *config.Config) (*paidruns.Gate, error) {
		g, err := open(cfg)
		if err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		err = g.Store.Update(context.Background(), func(st *paidruns.State) error {
			st.Tiers["claude"] = paidrules.TierResult{Tier: paidrules.TierLarge, Limit: 10}
			st.Reservations["r-1"] = paidrules.Reservation{ID: "r-1", Agent: "claude", Created: now.Add(-time.Minute),
				Estimate: paidrules.Estimate{Windows: []paidrules.WindowCost{{Key: paidrules.WindowKey{Kind: "session", Reset: now.Add(time.Hour)}, Used: 0.41}}}}
			return nil
		})
		return g, err
	}
	line := leadLoad(d, newRootCommand(d))(context.Background(), "lead-1").Line()
	if !strings.Contains(line, "claude 1 running (1 idle) of 10") || !strings.Contains(line, "5h 41%") {
		t.Fatalf("line %q, want the large tier's 10 and the admission's 41%%", line)
	}
}

// The load line of the lead's hooks reads no quota, and it waits for Rex no
// longer than its deadline: a pane that does not answer leaves the line out
// and does not hold the person's prompt.
func TestLeadLoadReadsNoQuotaAndKeepsItsDeadline(t *testing.T) {
	clearModelMarkers(t)
	root, _ := peerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	recordListPeer(t, root)
	d := testDeps(t)
	reads := 0
	open := d.Delegate.PaidGate
	d.Delegate.PaidGate = func(cfg *config.Config) (*paidruns.Gate, error) {
		g, err := open(cfg)
		if err != nil {
			return nil, err
		}
		read := g.Cache.Read
		g.Cache.Read = func(ctx context.Context, p workerenv.Process) (quota.Snapshot, error) {
			reads++
			return read(ctx, p)
		}
		return g, nil
	}
	cmd := newRootCommand(d)
	if line := leadLoad(d, cmd)(context.Background(), "lead-1").Line(); reads != 0 || !strings.Contains(line, "claude 1 running (1 idle)") {
		t.Fatalf("%d quota reads, line %q", reads, line)
	}
	testbin.Install(t, "rex", testbin.Scenario{Responses: map[string]testbin.Scenario{
		"block inspect": {Stdout: `{"block":{"window_id":"window:lead"}}`},
		"wait":          {SleepMS: 60000},
		"capture":       {SleepMS: 60000},
	}})
	started := time.Now()
	line := leadLoad(d, cmd)(context.Background(), "lead-1").Line()
	if took := time.Since(started); line != "" || took > loadWait+5*time.Second {
		t.Fatalf("line %q after %s", line, took)
	}
}
