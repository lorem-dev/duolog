package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/app/load"
	"github.com/lorem-dev/duolog/internal/app/peer"
	"github.com/lorem-dev/duolog/internal/workerenv"
	"github.com/spf13/cobra"
)

// loadTimeout bounds the account reads of the load line, so a slow quota read
// never holds a hook.
const loadTimeout = 10 * time.Second

// loadSummary counts peers against the session limits and the 5-hour window
// of each agent. It reuses the numbers `duolog quota` prints as
// "<agent> sessions N of M": the gate's report of the agent's free worker,
// which reads the same account state and the same cached quota read. An agent
// whose report fails has no limit and is left out. The result is empty on any
// failure.
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
	ctx, cancel := context.WithTimeout(ctx, loadTimeout)
	defer cancel()
	home, _ := os.UserHomeDir()
	environ := os.Environ()
	limits := map[string]load.Limits{}
	for _, agent := range load.Agents {
		report, _ := gate.Report(ctx, workerenv.ProcessInput{Agent: agent, Shape: workerenv.Worker,
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
		}
		limits[agent] = l
	}
	counted := make([]load.Peer, 0, len(peers))
	for _, p := range peers {
		live := p.State != peer.Exited && p.State != peer.Gone && p.State != peer.Closed
		counted = append(counted, load.Peer{ID: p.ID, Agent: p.Agent, Live: live, Idle: live && p.State == peer.Ready})
	}
	return load.Build(counted, limits)
}

// writeLoadLine prints the load line as the last line of `peer list`, when
// there is one.
func writeLoadLine(ctx context.Context, d *cmdDeps, w io.Writer, peers []peer.ListedPeer) error {
	root, err := projectRoot()
	if err != nil {
		return nil //nolint:nilerr // the load line is extra; the list is already printed
	}
	line := loadSummary(ctx, d, peers).Line(load.QueueEmpty(filepath.Join(root, ".duolog")))
	if line == "" {
		return nil
	}
	_, err = fmt.Fprintln(w, line)
	return err
}

// leadLoad gives the lead hooks the load of the lead with the given session
// id. A failure gives an empty summary: a hook never fails the session.
func leadLoad(d *cmdDeps, cmd *cobra.Command) func(ctx context.Context, leadID string) load.Summary {
	return func(ctx context.Context, leadID string) load.Summary {
		cmd.SetContext(ctx)
		pctx, cancel, err := peerContext(d, cmd, leadID, false, 0)
		if err != nil {
			return load.Summary{}
		}
		defer cancel()
		peers, err := peer.List(pctx)
		if err != nil {
			return load.Summary{}
		}
		return loadSummary(ctx, d, peers)
	}
}
