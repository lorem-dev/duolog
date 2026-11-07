package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/app/load"
	"github.com/lorem-dev/duolog/internal/app/peer"
	"github.com/lorem-dev/duolog/internal/workerenv"
	"github.com/spf13/cobra"
)

// loadWait bounds the whole load line of a lead hook, its Rex reads
// included, so that it never holds the person's prompt for long; a line not
// done by then is left out.
const loadWait = 2 * time.Second

// windowAge is the oldest window use the load line shows when the cache has
// no read: the one the latest admission measured. An older one is unknown.
const windowAge = 30 * time.Minute

// loadSummary counts peers against the session limits and the 5-hour window
// of each agent, and the lead's queued items. It reuses the numbers `duolog
// quota` prints as "<agent> sessions N of M": the gate's report of the agent's
// free worker, which reads the same account state. It reads no quota: the
// report judges from the agent's latest read in the cache, and without one
// the limit is that of the tier admission recorded last and the window the
// one the latest admission measured, at most windowAge ago. An agent whose
// report fails has no limit and is left out. The result is empty on any
// failure. ctx carries the lead's peer options.
func loadSummary(ctx context.Context, d *cmdDeps, peers []peer.ListedPeer) load.Summary {
	root, err := projectRoot()
	if err != nil {
		return load.Summary{}
	}
	cfg, err := configfile.Load(root)
	if err != nil {
		return load.Summary{}
	}
	gate, err := d.Delegate.PaidGate(cfg)
	if err != nil {
		return load.Summary{}
	}
	home, _ := os.UserHomeDir()
	environ := os.Environ()
	limits := map[string]load.Limits{}
	for _, agent := range load.Agents {
		report, _ := gate.ReportCached(ctx, workerenv.ProcessInput{Agent: agent, Shape: workerenv.Worker,
			Variant: workerenv.Free, Config: cfg, Environ: environ,
			Locations: workerenv.Locations{Home: home, Root: root, GOOS: runtime.GOOS, Environ: environ}})
		if report.Assessment.Agent == "" {
			continue
		}
		l := load.Limits{Limit: report.Sessions.Limit, Live: report.Sessions.Live}
		if s, ok := gate.Cache.Latest(agent); ok {
			for _, w := range s.Windows {
				if w.Kind == "session" {
					l.Window, l.WindowKnown = w.UsedPercent, true
				}
			}
		} else if used, known, err := gate.SessionWindow(ctx, agent, windowAge); err == nil {
			l.Window, l.WindowKnown = used, known
		}
		limits[agent] = l
	}
	counted := make([]load.Peer, 0, len(peers))
	for _, p := range peers {
		live := p.State != peer.Exited && p.State != peer.Gone && p.State != peer.Closed
		counted = append(counted, load.Peer{ID: p.ID, Agent: p.Agent, Live: live, Idle: live && p.State == peer.Ready})
	}
	s := load.Build(counted, limits)
	s.Queued = queuedItems(ctx)
	return s
}

// queuedItems counts the items of the lead's ready queue that wait to open,
// as `duolog peer queue list` shows them queued. A queue it cannot read
// counts none.
func queuedItems(ctx context.Context) int {
	items, err := peer.QueueList(ctx)
	if err != nil {
		return 0
	}
	n := 0
	for _, item := range items {
		if item.State == peer.QueueQueued {
			n++
		}
	}
	return n
}

// writeLoadLine prints the load line as the last line of `peer list`, when
// there is one. ctx carries the lead's peer options.
func writeLoadLine(ctx context.Context, d *cmdDeps, w io.Writer, peers []peer.ListedPeer) error {
	line := loadSummary(ctx, d, peers).Line()
	if line == "" {
		return nil
	}
	_, err := fmt.Fprintln(w, line)
	return err
}

// queueStarter starts the lead's queue run for the lead hooks: on a prompt
// and when a peer-idle message reaches the lead. It starts one only while
// the load line, from loads, shows a free session for a queued item's agent;
// an item left opening by a run that ended starts one whatever the line
// says. One hook process starts at most one run per lead; the run itself
// opens only what admission allows. A failure starts nothing: a hook never
// fails the session.
func queueStarter(d *cmdDeps, cmd *cobra.Command, loads func(ctx context.Context, leadID string) load.Summary) func(ctx context.Context, leadID string) {
	started := map[string]bool{}
	return func(ctx context.Context, leadID string) {
		if started[leadID] {
			return
		}
		started[leadID] = true
		summary := loads(ctx, leadID)
		cmd.SetContext(ctx)
		pctx, cancel, err := peerContext(d, cmd, leadID, false, 0)
		if err != nil {
			return
		}
		defer cancel()
		_, _ = d.StartQueueRun(pctx, func(agent string) bool { return summary.Free(agent) > 0 })
	}
}

// leadLoad gives the lead hooks the load of the lead with the given session
// id, within loadWait. One hook process computes it once per lead, so the
// line it shows and the queue start it decides agree. A failure gives an
// empty summary: a hook never fails the session.
func leadLoad(d *cmdDeps, cmd *cobra.Command) func(ctx context.Context, leadID string) load.Summary {
	known := map[string]load.Summary{}
	return func(ctx context.Context, leadID string) load.Summary {
		if s, ok := known[leadID]; ok {
			return s
		}
		known[leadID] = load.Summary{}
		cmd.SetContext(ctx)
		pctx, cancel, err := peerContext(d, cmd, leadID, false, loadWait)
		if err != nil {
			return load.Summary{}
		}
		defer cancel()
		peers, err := peer.List(pctx)
		if err != nil {
			return load.Summary{}
		}
		known[leadID] = loadSummary(pctx, d, peers)
		return known[leadID]
	}
}
