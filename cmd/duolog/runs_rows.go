package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"text/tabwriter"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/peer"
	"github.com/spf13/cobra"
)

// Kinds of worker that duolog runs lists.
const (
	kindPeer     = "peer"
	kindDelegate = "delegate"
)

// runRow is one worker of the project, a live peer or a delegate run. It is
// the one shape of duolog runs --json, so every key is always present: a value
// that is not known is "" (started is null).
type runRow struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Role  string `json:"role"`
	Agent string `json:"agent"`
	Model string `json:"model"`
	// State is the state scripts read: a delegate run's own, a peer's as in
	// duolog peer list --json.
	State string `json:"state"`
	// Started is when a delegate run began; duolog does not record it for a
	// peer, so it is null there.
	Started *time.Time `json:"started"`
	// Worktree is the absolute path of the work root; TaskDir is the absolute
	// path of the folder of the run's task and log, "" when none is known.
	Worktree string `json:"worktree"`
	TaskDir  string `json:"task_dir"`

	// display is the state a person reads in the table; target is its AGENT cell.
	display, target string
}

// delegateRow is the row of a delegate record.
func delegateRow(r delegate.Record) runRow {
	row := runRow{ID: r.ID, Kind: kindDelegate, Role: r.Role, Agent: r.Agent, Model: r.Model, State: r.State, Worktree: r.WorkRoot, display: r.State, target: r.Target()}
	if !r.Started.IsZero() {
		started := r.Started
		row.Started = &started
	}
	switch {
	case r.Log != "":
		row.TaskDir = underWork(r.WorkRoot, filepath.Dir(filepath.FromSlash(r.Log)))
	case r.TaskPath != "":
		row.TaskDir = underWork(r.WorkRoot, filepath.Dir(filepath.FromSlash(r.TaskPath)))
	}
	return row
}

// peerRow is the row of a listed peer; its state is the one peer list gives.
func peerRow(p peer.ListedPeer) runRow {
	row := runRow{ID: p.ID, Kind: kindPeer, Role: p.Role, Agent: p.Agent, State: string(p.State), Worktree: p.WorkRoot, display: p.Display(), target: p.Agent}
	if p.Task != "" {
		row.TaskDir = underWork(p.WorkRoot, filepath.Dir(filepath.FromSlash(p.Task)))
	}
	return row
}

// underWork is dir under work when dir is relative.
func underWork(work, dir string) string {
	if filepath.IsAbs(dir) || work == "" {
		return dir
	}
	return filepath.Join(work, dir)
}

// listPeers returns the project's live peers, or none where peers do not
// exist: on Windows and outside Rex.
func (d *cmdDeps) listPeers(cmd *cobra.Command, lead string) ([]peer.ListedPeer, error) {
	if runtime.GOOS == "windows" || os.Getenv("REX_SESSION") == "" {
		return nil, nil
	}
	ctx, cancel, err := peerContext(d, cmd, lead, false, 0)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return peer.List(ctx)
}

// runRows is the live peers, then the shown delegate runs. A peer list that
// cannot be read leaves the delegate runs and says so on stderr.
func (d *cmdDeps) runRows(cmd *cobra.Command, stateDir, lead string) ([]runRow, error) {
	records, err := listRuns(stateDir)
	if err != nil {
		return nil, err
	}
	var rows []runRow
	peers, err := d.listPeers(cmd, lead)
	if err != nil {
		if _, werr := fmt.Fprintf(cmd.ErrOrStderr(), "peers not listed: %v\n", err); werr != nil {
			return nil, werr
		}
	}
	for _, p := range peers {
		rows = append(rows, peerRow(p))
	}
	for _, r := range shownRuns(records) {
		rows = append(rows, delegateRow(r))
	}
	return rows, nil
}

// writeRunRows prints "<id> <state>" lines, the table with long, or with asJSON
// the rows as one JSON list.
func writeRunRows(w io.Writer, rows []runRow, long, asJSON bool, now time.Time) error {
	if asJSON {
		if rows == nil {
			rows = []runRow{}
		}
		return json.NewEncoder(w).Encode(rows)
	}
	if !long {
		for _, r := range rows {
			if _, err := fmt.Fprintf(w, "%s %s\n", r.ID, r.State); err != nil {
				return err
			}
		}
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ID\tKIND\tROLE\tAGENT\tSTATE\tAGE\tWORKTREE"); err != nil {
		return err
	}
	for _, r := range rows {
		age := "-"
		if r.Started != nil {
			age = agoShort(now.Sub(*r.Started))
		}
		tree := ""
		if r.Worktree != "" {
			tree = filepath.Base(r.Worktree)
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.Kind, r.Role, dash(r.target), r.display, age, dash(tree)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// peerKnown reports whether the registry in stateDir holds a live peer id. It
// reads the record only: no Rex call, no lead.
func peerKnown(stateDir, id string) bool {
	reg, err := session.Open(stateDir)
	if err != nil {
		return false
	}
	entries, err := reg.Entries()
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.ID == id && e.Block != "" {
			return true
		}
	}
	return false
}
