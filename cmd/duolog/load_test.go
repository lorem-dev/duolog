package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/testbin"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

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
