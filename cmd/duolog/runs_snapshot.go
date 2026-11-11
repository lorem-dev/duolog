package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/load"
	"github.com/lorem-dev/duolog/internal/app/peer"
	"github.com/lorem-dev/duolog/internal/app/runsnap"
	corerunsnap "github.com/lorem-dev/duolog/internal/core/runsnap"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/spf13/cobra"
)

// writeRunsSnapshot prints duolog runs --snapshot: the lead's snapshot as one
// JSON line. A worker is refused, as for runs.
func (d *cmdDeps) writeRunsSnapshot(cmd *cobra.Command, lead string, fast bool) error {
	if isWorker() {
		return errWorkerRuns
	}
	root, err := projectRoot()
	if err != nil {
		return err
	}
	s, err := d.runsSnapshot(cmd.Context(), root, lead, fast, time.Now(), d.snapDeps)
	if err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = cmd.OutOrStdout().Write(append(data, '\n'))
	return err
}

// runsSnapshot is the snapshot of the lead of the project at root, read at
// now. It creates no state: with no lead home it is the empty snapshot. With
// no lead it shows no peer and no run, never another lead's; a lead that was
// asked for (lead, else DUOLOG_LEAD) and does not resolve is named in its
// error. The load is from the cache only. With fast, no pane screen is read.
// depsOf builds the ports for the lead home's registry.
func (d *cmdDeps) runsSnapshot(ctx context.Context, root, lead string, fast bool, now time.Time, depsOf func(*session.Registry) runsnap.Deps) (corerunsnap.Snapshot, error) {
	asked := lead
	if asked == "" {
		asked = session.CurrentLead(os.Environ())
	}
	home := project.LeadHome(root)
	// session.Open would create the lead home.
	if !isFolder(home) {
		s := runsnap.Gathered{Root: root}.Snapshot(now)
		s.Error = snapshotLeadMiss(asked, nil)
		return s, nil
	}
	reg, err := session.Open(home)
	if err != nil {
		return corerunsnap.Snapshot{}, err
	}
	s, err := runsnap.Build(ctx, depsOf(reg), runsnap.Options{LeadID: lead, Fast: fast, Now: now})
	if err != nil || s.Lead != nil {
		return s, err
	}
	s.Error = snapshotLeadMiss(asked, reg)
	return s, nil
}

// isFolder reports whether path is a folder that can be read.
func isFolder(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// snapshotLeadMiss is the error of a snapshot whose lead did not resolve:
// the lead that was asked for, or a registry reg that cannot be read; ""
// when none was asked for (no live lead, or several). It names no path.
func snapshotLeadMiss(asked string, reg *session.Registry) string {
	if asked != "" {
		return "lead: " + asked + " is not a live lead"
	}
	if reg != nil {
		if _, err := reg.Entries(); err != nil {
			return "lead: the session registry cannot be read"
		}
	}
	return ""
}

// snapDeps are the ports of a gathering of the lead home whose registry is
// reg: peers through Rex with this project's peer options, the delegate
// records across the lead home, and the load line's counts. A caller that
// shows no load sets Load to nil, so that none is computed.
func (d *cmdDeps) snapDeps(reg *session.Registry) runsnap.Deps {
	return d.snapDepsFor(reg, "")
}

// snapDepsAt is snapDeps for the work project at work, not the working
// directory's. Its peers and load are read only when that project has a
// .duolog folder, so that nothing creates one there or elsewhere.
func (d *cmdDeps) snapDepsAt(reg *session.Registry, work string) runsnap.Deps {
	return d.snapDepsFor(reg, project.StateRoot(work))
}

// snapDepsFor builds the ports for the state root at root, or for the working
// directory's project when root is "".
func (d *cmdDeps) snapDepsFor(reg *session.Registry, root string) runsnap.Deps {
	hasState := root == "" || isFolder(filepath.Join(root, ".duolog"))
	deps := runsnap.Deps{
		Registry: reg,
		Peers: func(ctx context.Context, leadID string, fast bool) ([]peer.ListedPeer, error) {
			if !hasState {
				return nil, peer.ErrNoRex
			}
			return d.snapPeers(ctx, leadID, fast, root)
		},
		Records: func(leadID, address string) ([]delegate.LocatedRecord, error) {
			at := root
			if at == "" {
				var err error
				if at, err = projectRoot(); err != nil {
					return nil, err
				}
			}
			return listRuns(reg, at, leadID, address)
		},
		Load: func(ctx context.Context, leadID string, peers []peer.ListedPeer) load.Summary {
			if !hasState {
				return load.Summary{}
			}
			return d.snapLoad(ctx, leadID, peers, root)
		},
	}
	return deps
}

// snapPeers lists the lead's live peers of the project at root, or of the
// working directory's when root is "". Peers need Rex and do not exist on
// Windows: there it is peer.ErrNoRex.
func (d *cmdDeps) snapPeers(ctx context.Context, leadID string, fast bool, root string) ([]peer.ListedPeer, error) {
	if runtime.GOOS == "windows" || os.Getenv("REX_SESSION") == "" {
		return nil, peer.ErrNoRex
	}
	pctx, cancel, err := d.peerCtxAt(ctx, leadID, false, 0, root)
	if err != nil {
		return nil, err
	}
	defer cancel()
	if fast {
		return peer.ListFast(pctx)
	}
	return peer.List(pctx)
}

// snapLoad counts the listed peers against the session limits, within
// loadWait; a failure gives an empty summary. A panic does too: the MCP
// server runs the status tool with no recover of its own, and a panic there
// would end the server, as hook.computeLoad keeps it from ending a hook.
func (d *cmdDeps) snapLoad(ctx context.Context, leadID string, peers []peer.ListedPeer, root string) (s load.Summary) {
	defer func() {
		if recover() != nil {
			s = load.Summary{}
		}
	}()
	pctx, cancel, err := d.peerCtxAt(ctx, leadID, false, loadWait, root)
	if err != nil {
		return load.Summary{}
	}
	defer cancel()
	if root != "" {
		return loadSummaryAt(pctx, d, root, peers)
	}
	return loadSummary(pctx, d, peers)
}
