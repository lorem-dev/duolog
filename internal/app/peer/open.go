package peer

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/mode"
	corepane "github.com/lorem-dev/duolog/internal/core/pane"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/store"
)

func newID() string { return "peer-" + strings.ToLower(rand.Text()[:6]) }

// Open starts a peer for the role, or returns the live one. It records the ID
// right after rex split, then waits until the session is ready, asks a
// question, has ended or the start time is over. The pane is never closed here.
func Open(ctx context.Context, role string) (opened Entry, err error) {
	started, agent, run := time.Now(), "", ""
	defer func() { observeOperation(ctx, "open", agent, run, started, opened.State, err) }()
	e, err := setup(ctx)
	if err != nil {
		return Entry{}, err
	}
	lead, err := e.lead(time.Now())
	if err != nil {
		return Entry{}, err
	}
	unavailable, err := e.reg.Unavailable()
	if err != nil {
		return Entry{}, err
	}
	route, err := coreroute.Resolve(e.cfg, role, unavailable, coreroute.Options{Lead: lead.target, AllowManualModel: e.opts.AllowManualModel,
		Balance: paidruns.DefaultPaceBalance(ctx, e.cfg)})
	if err != nil {
		return Entry{}, err
	}
	paidruns.SaveDefaultPaceState(route.Ahead)
	agent = route.Agent
	if lead.mode == mode.Plan && route.Sandbox != "read-only" {
		return Entry{}, fmt.Errorf("mode plan refuses write role %s", role)
	}
	if route.InLead {
		here := delegate.Placement{Where: "subagent", Model: route.Model, Effort: route.Effort}
		return Entry{State: Limited, Note: strings.TrimSpace(strings.TrimSpace(here.String()) + " " + route.Reason)}, nil
	}
	// Serialize placement across worktrees and agents sharing this registry.
	// Release before watching startup so another peer can open while this one works.
	unlock, err := store.LockFile(filepath.Join(e.dir, "peer-open.lock"))
	if err != nil {
		return Entry{}, err
	}
	defer func() { _ = unlock() }()
	peers, err := e.livePeers(ctx, lead.id, route.Agent)
	if err != nil {
		return Entry{}, err
	}
	for _, p := range peers {
		if p.Role == role && sameWorkRoot(p.WorkRoot, e.root) {
			run = p.Run
			if err := unlock(); err != nil {
				return Entry{}, err
			}
			return e.watch(ctx, p, true)
		}
	}
	var here []session.Entry
	for _, p := range peers {
		if sameWorkRoot(p.WorkRoot, e.root) {
			here = append(here, p)
		}
	}
	if limit := e.cfg.Limits.MaxLiveSessions; limit > 0 && len(here) >= limit {
		return Entry{}, fmt.Errorf("max_live_sessions is %d and %s session %s is still open; close it with duolog peer close %s first", limit, route.Agent, here[0].ID, here[0].ID)
	}
	agentPane := e.pane(route.Agent)
	if agentPane.Agent == nil {
		return Entry{}, fmt.Errorf("peer open: %s has no pane support", route.Agent)
	}
	resolver, ok := e.opts.Deps.Models[route.Agent]
	if !ok {
		return Entry{}, fmt.Errorf("unknown agent %q", route.Agent)
	}
	model, err := resolver.Resolve(ctx, e.cfg, filepath.Join(e.dir, "cache"), route.Target)
	if err != nil {
		return Entry{}, err
	}
	route.Model = model
	exe, err := os.Executable()
	if err != nil {
		return Entry{}, err
	}
	// The first launch of the agent in this lead says which billing keys the
	// pane no longer gets; delegate shares the claim.
	notices, err := delegate.ClaimMigration(e.opts.Deps.DroppedKeys, e.reg, e.cfg, lead.id, route.Agent)
	if err != nil {
		return Entry{}, err
	}
	noted := withNotice(strings.TrimSpace(strings.Join(append(notices, route.Reason), " ")))
	// The ID goes into the pane's environment, so it is chosen before the pane exists.
	id := newID()
	// So is the run id, the pane's DUOLOG_RUN.
	run = delegate.NewRunID()
	// The account gate admits the pane before it exists: one live slot and its
	// first task, in the user's account state shared by every lead and
	// project. The env file and the argv come from the process it built.
	pane, err := e.admitPane(ctx, route, lead, role, id, run)
	if err != nil {
		return noted(Entry{}, err)
	}
	envFile, err := writeEnvFile(e.dir, pane.permit.Process.Env)
	if err != nil {
		return noted(Entry{}, errors.Join(err, pane.release(ctx)))
	}
	if err := pane.launch(ctx, envFile); err != nil {
		_ = os.Remove(envFile)
		return noted(Entry{}, errors.Join(err, pane.release(ctx)))
	}
	block, rexSession, err := e.openProjectPeer(ctx, tabLabel(route.Agent, role), launchArgv(exe, envFile, agentPane.Agent.Argv(route, lead.mode, pane.permit.Process)))
	e.opts.Deps.OnEnvFile(envFile)
	if err != nil {
		// Rex's own refusal shows that no pane holds the reservation. So does
		// an env file still there to take: duolog peer launch starts nothing
		// without it, and one that read it first cannot remove it after us.
		// A file already taken means the agent may run: the hold stays.
		if removeErr := os.Remove(envFile); e.panes.Refused(err) || removeErr == nil {
			err = errors.Join(err, pane.release(ctx))
		}
		return noted(Entry{}, err)
	}
	entry := session.Entry{ID: id, Lead: lead.id, Agent: route.Agent, Role: role, Thread: peerThread, Block: block, WorkRoot: e.root, RexSession: rexSession, Run: run, Mode: lead.mode, UpdatedAt: time.Now(),
		AccountSessionID: pane.id(), AdmissionID: pane.id()}
	attachErr := pane.attach(ctx, block, rexSession)
	if attachErr == nil {
		attachErr = e.opts.Deps.RecordPeer(e.reg, entry, e.cfg.Limits.MaxLiveSessions)
	}
	if attachErr != nil {
		// The pane exists but nothing records it: do not leave it unowned. Its
		// holds go only once its close is confirmed; otherwise they stay.
		closeErr := e.panes.Close(ctx, e.paneOf(entry))
		if closeErr == nil {
			closeErr = e.sessionEnded(ctx, entry)
		}
		cleanupErr := e.cleanupProjectSession(ctx)
		return noted(Entry{}, fmt.Errorf("pane %s was opened but not recorded: %w", block, errors.Join(attachErr, closeErr, cleanupErr)))
	}
	// Keep the resolved version across commands; aliases may resolve differently
	// by the time this pane first changes a file.
	if e.cfg.Coauthors.Enabled {
		if err := os.WriteFile(e.modelPath(entry), []byte(model), 0600); err != nil {
			return noted(Entry{}, err)
		}
	}
	if err := unlock(); err != nil {
		return noted(Entry{}, err)
	}
	return noted(e.watch(ctx, entry, true))
}

// withNotice adds a claimed notice to what Open reports, so that it is not
// lost: to an error, or to the question, which the command then prints alone,
// or else to the note.
func withNotice(notice string) func(Entry, error) (Entry, error) {
	return func(entry Entry, err error) (Entry, error) {
		switch {
		case notice == "":
		case err != nil:
			err = fmt.Errorf("%w; %s", err, notice)
		case entry.Question != "":
			entry.Question += "; " + notice
		case entry.Note != "":
			entry.Note += "; " + notice
		default:
			entry.Note = notice
		}
		return entry, err
	}
}

// livePeers returns the open panes of this lead and agent. A pane that is gone
// or whose program ended is marked closed on the way, so it cannot hold the limit.
func (e *env) livePeers(ctx context.Context, leadID, agent string) ([]session.Entry, error) {
	all, err := e.reg.Entries()
	if err != nil {
		return nil, err
	}
	var live []session.Entry
	for _, x := range all {
		if x.Block == "" || x.Closed || x.Lead != leadID || x.Agent != agent {
			continue
		}
		alive, err := e.alive(ctx, x)
		if err != nil {
			return nil, err
		}
		if !alive {
			if _, err := e.markEnded(x, Closed); err != nil {
				return nil, err
			}
			continue
		}
		live = append(live, x)
	}
	return live, nil
}

// alive reports whether the program in a pane still runs. It captures at most
// capture_lines lines to confirm that the pane answers, and drops them.
func (e *env) alive(ctx context.Context, entry session.Entry) (bool, error) {
	id := e.paneOf(entry)
	presence, err := e.panes.Probe(ctx, id)
	if err != nil || presence != corepane.Running {
		return false, err
	}
	if _, err := e.panes.Capture(ctx, id, e.cfg.Rex.CaptureLines); err != nil {
		if e.panes.Gone(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
