package runsnap

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/load"
	"github.com/lorem-dev/duolog/internal/app/peer"
	corerunsnap "github.com/lorem-dev/duolog/internal/core/runsnap"
)

// Build is the snapshot of one gathering, read at o.Now. It shows only the
// lead's own workers: when no lead resolves it reads no part and shows no
// peer and no run.
func Build(ctx context.Context, d Deps, o Options) (corerunsnap.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return corerunsnap.Snapshot{}, err
	}
	g := withLead(d, o)
	if g.Lead != nil {
		var err error
		if g, err = gatherParts(ctx, d, g, o); err != nil {
			return corerunsnap.Snapshot{}, err
		}
	}
	return g.Snapshot(o.Now), nil
}

// Snapshot is the gathering as snapshot version 1, read at now: the runs
// FreshRuns keeps, and every path relative to Root. No lead is no error: the
// person may run it with none.
func (g Gathered) Snapshot(now time.Time) corerunsnap.Snapshot {
	s := corerunsnap.Snapshot{
		Version:     corerunsnap.Version,
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Rex:         corerunsnap.Rex{Available: g.Rex},
		Peers:       []corerunsnap.Peer{},
		Runs:        []corerunsnap.Run{},
		AsksOpen:    len(g.Asks),
		Load:        snapLoad(g.Load),
	}
	if g.Lead != nil {
		s.Lead = &corerunsnap.Lead{ID: g.Lead.ID, Agent: g.Lead.Agent}
	}
	for _, p := range g.Peers {
		s.Peers = append(s.Peers, snapPeer(g.Root, p))
	}
	for _, r := range FreshRuns(g.Records, now) {
		s.Runs = append(s.Runs, snapRun(g.Root, r))
	}
	var errs []string
	if g.PeersErr != nil {
		errs = append(errs, "peers: "+g.PeersErr.Error())
	}
	if g.RecordsErr != nil {
		errs = append(errs, "runs: "+g.RecordsErr.Error())
	}
	s.Error = withoutRoot(g.Root, strings.Join(errs, "; "))
	s.LineText = s.Line()
	return s
}

// withoutRoot is text with each spelling of root (as given, and with links
// resolved) made relative: "<root>/a" reads "a" and "<root>" reads ".". An
// error's text keeps no absolute path of the project then.
func withoutRoot(root, text string) string {
	if root == "" || text == "" {
		return text
	}
	spellings := []string{root}
	if resolved, err := filepath.EvalSymlinks(root); err == nil && resolved != root {
		spellings = append(spellings, resolved)
	}
	// The longer spelling first: the shorter may be a part of it.
	slices.SortFunc(spellings, func(a, b string) int { return len(b) - len(a) })
	for _, s := range spellings {
		if s = strings.TrimRight(s, `/\`); s == "" {
			continue
		}
		text = strings.ReplaceAll(text, s+string(filepath.Separator), "")
		text = strings.ReplaceAll(text, s+"/", "")
		text = strings.ReplaceAll(text, s, ".")
	}
	return text
}

// snapPeer is a listed peer in the snapshot. Its task is relative to its
// work root unless absolute.
func snapPeer(root string, p peer.ListedPeer) corerunsnap.Peer {
	task := p.Task
	if task != "" && !filepath.IsAbs(task) && p.WorkRoot != "" {
		task = filepath.Join(p.WorkRoot, filepath.FromSlash(task))
	}
	out := corerunsnap.Peer{ID: p.ID, Role: p.Role, Agent: p.Agent, State: string(p.State), Working: p.Working,
		Task: rel(root, task), Tab: p.Tab, Worktree: rel(root, p.WorkRoot)}
	if p.Last != nil {
		out.Last = &corerunsnap.Last{Kind: p.Last.Kind, At: timeOrNil(p.Last.At)}
	}
	return out
}

// snapRun is a delegate record in the snapshot.
func snapRun(root string, r delegate.LocatedRecord) corerunsnap.Run {
	return corerunsnap.Run{ID: r.ID, Role: r.Role, Agent: r.Agent, Model: r.Model, State: r.State,
		Started: timeOrNil(r.Started), Ended: timeOrNil(r.Ended), Worktree: rel(root, r.WorkRoot),
		TaskDir: rel(root, r.TaskFolder()), HasLog: hasLog(r.Record), Line: r.Line}
}

// snapLoad is the load summary in the snapshot.
func snapLoad(l load.Summary) corerunsnap.Load {
	out := corerunsnap.Load{Agents: []corerunsnap.AgentLoad{}, Queued: l.Queued}
	for _, a := range l.Agents {
		out.Agents = append(out.Agents, corerunsnap.AgentLoad{Agent: a.Name, Running: a.Running, Idle: a.Idle, Limit: a.Limit, Live: a.Live})
	}
	if l.WindowKnown {
		out.Window5h = &l.Window
	}
	if l.WeeklyKnown {
		out.Weekly = &l.Weekly
	}
	return out
}

// hasLog reports whether the record's log file exists. Its path is relative
// to the work root unless absolute.
func hasLog(r delegate.Record) bool {
	if r.Log == "" {
		return false
	}
	log := filepath.FromSlash(r.Log)
	if !filepath.IsAbs(log) {
		if r.WorkRoot == "" {
			return false
		}
		log = filepath.Join(r.WorkRoot, log)
	}
	info, err := os.Stat(log)
	return err == nil && info.Mode().IsRegular()
}

// timeOrNil is t in UTC, nil when zero.
func timeOrNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	t = t.UTC()
	return &t
}

// rel is abs relative to root with "/" separators; "" when abs is "" or has
// no relative path from root. A path outside root under one spelling may be
// inside it under the other (a symlinked temp folder): then the resolved
// spellings give the path.
func rel(root, abs string) string {
	if abs == "" {
		return ""
	}
	r, err := filepath.Rel(root, abs)
	if err != nil {
		return ""
	}
	if !filepath.IsLocal(r) && r != "." {
		rr, rerr := filepath.EvalSymlinks(root)
		ra, aerr := filepath.EvalSymlinks(abs)
		if rerr == nil && aerr == nil {
			if resolved, err := filepath.Rel(rr, ra); err == nil && (filepath.IsLocal(resolved) || resolved == ".") {
				r = resolved
			}
		}
	}
	return filepath.ToSlash(r)
}
