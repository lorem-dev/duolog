package peer

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/mode"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
)

func newID() string { return "peer-" + strings.ToLower(rand.Text()[:6]) }

// route resolves where role runs for lead, as Open places it. Both agents
// limited, and a lead in plan mode with a role that writes, refuse the open
// for now (ErrRefused): the same open may pass once a limit resets or the
// lead leaves plan mode. The route comes back with the plan refusal, so the
// caller knows its agent.
func (e *env) route(ctx context.Context, role string, lead leadInfo) (coreroute.Route, error) {
	unavailable, err := e.reg.Unavailable()
	if err != nil {
		return coreroute.Route{}, err
	}
	route, err := coreroute.Resolve(e.cfg, role, unavailable, coreroute.Options{Lead: lead.target, AllowManualModel: e.opts.AllowManualModel,
		Balance: paidruns.DefaultPaceBalance(ctx, e.cfg)})
	if errors.Is(err, coreroute.ErrNoAgent) {
		return coreroute.Route{}, refused(err.Error())
	}
	if err != nil {
		return coreroute.Route{}, err
	}
	paidruns.SaveDefaultPaceState(route.Ahead)
	if lead.mode == mode.Plan && route.Sandbox != "read-only" {
		return route, refused(fmt.Sprintf("mode plan refuses write role %s", role))
	}
	return route, nil
}

// Open starts a peer for the role, or returns the live one, unless the
// options ask for a new pane only. It records the ID
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
	route, err := e.route(ctx, role, lead)
	agent = route.Agent
	if err != nil {
		return Entry{}, err
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
	if p, ok := e.reusable(peers, role); ok {
		run = p.Run
		if err := unlock(); err != nil {
			return Entry{}, err
		}
		return e.watch(ctx, p, true)
	}
	var here []session.Entry
	for _, p := range peers {
		if sameWorkRoot(p.WorkRoot, e.root) {
			here = append(here, p)
		}
	}
	if limit := e.cfg.Limits.MaxLiveSessions; limit > 0 && len(here) >= limit {
		return Entry{}, refused(fmt.Sprintf("max_live_sessions is %d and %s session %s is still open; close it with duolog peer close %s first", limit, route.Agent, here[0].ID, here[0].ID))
	}
	model, err := e.opts.Deps.ResolveModel(ctx, e.cfg, filepath.Join(e.dir, "cache"), route.Target)
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
	notices, err := delegate.ClaimMigration(e.reg, e.cfg, lead.id, route.Agent)
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
	block, rexSession, err := e.openProjectPeer(ctx, tabLabel(route.Agent, role), launchArgv(exe, envFile, agentArgv(route, lead.mode, pane.permit.Process)))
	e.opts.Deps.OnEnvFile(envFile)
	if err != nil {
		// Rex's own refusal shows that no pane holds the reservation. So does
		// an env file still there to take: duolog peer launch starts nothing
		// without it, and one that read it first cannot remove it after us.
		// A file already taken means the agent may run: the hold stays.
		if removeErr := os.Remove(envFile); paneAbsent(err) || removeErr == nil {
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
		closeErr := e.clientFor(entry).Close(ctx, block)
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

// reusable is the role's live peer in this checkout, among peers, which Open
// returns instead of a new pane; none when the options ask for a new pane.
func (e *env) reusable(peers []session.Entry, role string) (session.Entry, bool) {
	at := slices.IndexFunc(peers, func(p session.Entry) bool { return p.Role == role && sameWorkRoot(p.WorkRoot, e.root) })
	if e.opts.NewOnly || at < 0 {
		return session.Entry{}, false
	}
	return peers[at], true
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
		alive, err := e.clientFor(x).Alive(ctx, x.Block, e.cfg.Rex.CaptureLines)
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
