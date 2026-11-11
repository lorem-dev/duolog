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

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/lead"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	corelead "github.com/lorem-dev/duolog/internal/core/lead"
	"github.com/lorem-dev/duolog/internal/core/mode"
	corepane "github.com/lorem-dev/duolog/internal/core/pane"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/run"
	corescreen "github.com/lorem-dev/duolog/internal/core/screen"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/store"
)

// newID is the ID of a new peer: the dependencies' choice, else a random one.
func (e *env) newID() string {
	if e.opts.Deps.NewID != nil {
		return e.opts.Deps.NewID()
	}
	return "peer-" + strings.ToLower(rand.Text()[:6])
}

// sessionName is the name a pane starts under: what it is, its role and its
// lead. A lead whose name cannot be read is named by its id; this never fails.
func (e *env) sessionName(role string, lead leadInfo) string {
	leadName := ""
	if read := e.opts.Deps.LeadName; read != nil && lead.target != nil {
		leadName = read(lead.target.Agent, lead.pid)
	}
	return corelead.SessionLabel("peer", role, leadName, lead.id)
}

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
		return coreroute.Route{}, admissionRefused(err.Error())
	}
	if err != nil {
		return coreroute.Route{}, err
	}
	paidruns.SaveDefaultPaceState(route.Ahead)
	if lead.mode == mode.Plan && route.Sandbox != "read-only" {
		return route, admissionRefused(fmt.Sprintf("mode plan refuses write role %s", role))
	}
	return route, nil
}

// Open starts a peer for the role, or returns the live one, unless the
// options ask for a new pane only. It records the ID
// right after rex split, then waits until the session is ready, asks a
// question, has ended or the start time is over. The pane is never closed here.
func Open(ctx context.Context, role string) (opened Entry, err error) {
	started, agent, run, reminder := time.Now(), "", "", ""
	defer endOpen(ctx, started, &agent, &run, &reminder, &opened, &err)
	e, err := setup(ctx)
	if err != nil {
		return Entry{}, submoduleOpenError(ctx, err)
	}
	lead, err := leadClaiming(e, &reminder)
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
	peers, group, err := e.capPeers(ctx, lead.id, route.Agent)
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
	// The group was resolved with the peers (capPeers).
	here := []session.Entry{}
	for _, p := range peers {
		if session.ShareCap(p, session.Entry{WorkRoot: e.root, CapGroup: group}) {
			here = append(here, p)
		}
	}
	if limit := e.maxLiveSessions; limit > 0 && len(here) >= limit {
		return Entry{}, admissionRefused(fmt.Sprintf("max_live_sessions is %d and %s session %s is still open; close it with duolog peer close %s first", limit, route.Agent, here[0].ID, here[0].ID))
	}
	agentPane := e.pane(route.Agent)
	if agentPane.Agent == nil {
		return Entry{}, fmt.Errorf("peer open: %s has no pane support", route.Agent)
	}
	resolver, ok := e.opts.Deps.Models[route.Agent]
	if !ok {
		return Entry{}, fmt.Errorf("unknown agent %q", route.Agent)
	}
	model, err := resolver.Resolve(ctx, e.cfg, filepath.Join(e.workDir, "cache"), route.Target)
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
	id := e.newID()
	// So is the run id, the pane's DUOLOG_RUN.
	run = delegate.NewRunID()
	// The account gate admits the pane before it exists: one live slot and its
	// first task, in the user's account state shared by every lead and
	// project. The env file and the argv come from the process it built.
	pane, err := e.admitPane(ctx, route, lead, role, id, run)
	if err != nil {
		return noted(Entry{}, err)
	}
	envFile, err := writeEnvFile(e.workDir, pane.permit.Process.Env)
	if err != nil {
		return noted(Entry{}, errors.Join(err, pane.release(ctx)))
	}
	if err := pane.launch(ctx, envFile); err != nil {
		_ = os.Remove(envFile)
		return noted(Entry{}, errors.Join(err, pane.release(ctx)))
	}
	block, rexSession, err := e.openProjectPeer(ctx, tabLabel(route.Agent, role), launchArgv(exe, envFile, agentPane.Agent.Argv(route, lead.mode, pane.permit.Process, e.sessionName(role, lead))))
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
	entry := session.Entry{ID: id, Lead: lead.id, Agent: route.Agent, Role: role, Thread: peerThread, Block: block, WorkRoot: e.root, CapGroup: group, RexSession: rexSession, Run: run, Mode: lead.mode, UpdatedAt: time.Now(),
		AccountSessionID: pane.id(), AdmissionID: pane.id()}
	attachErr := pane.attach(ctx, block, rexSession)
	if attachErr == nil {
		attachErr = e.opts.Deps.RecordPeer(e.reg, entry, e.maxLiveSessions)
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

// capPeers is livePeers with the cap group of a new pane in e.root, resolved
// before the registry is written, so that its lock never runs Git. A group
// that Git cannot work out refuses the open.
func (e *env) capPeers(ctx context.Context, leadID, agent string) ([]session.Entry, string, error) {
	peers, err := e.livePeers(ctx, leadID, agent)
	if err != nil {
		return nil, "", err
	}
	group, err := project.CapGroup(e.root, e.dir)
	return peers, group, err
}

// leadClaiming is the lead of Open, and claims into reminder the one-time
// reminder of a submodule's own rules for this checkout.
func leadClaiming(e *env, reminder *string) (leadInfo, error) {
	l, err := e.lead(time.Now())
	if err != nil {
		return l, err
	}
	*reminder, err = lead.ClaimSubmoduleReminder(e.reg, l.id, e.root)
	return l, err
}

// endOpen ends Open: it adds the submodule reminder to what Open returns,
// then reports the operation with its final state.
func endOpen(ctx context.Context, started time.Time, agent, run, reminder *string, opened *Entry, err *error) {
	*opened, *err = withNotice(*reminder)(*opened, *err)
	observeOperation(ctx, "open", *agent, *run, started, opened.State, *err)
}

func submoduleOpenError(ctx context.Context, err error) error {
	opts, _ := callerOptions(ctx)
	if opts.Root == "" {
		return err
	}
	work := project.CurrentWorkRoot(opts.Root)
	if opts.WorkDir != "" {
		work = project.WorkRoot(opts.WorkDir, opts.Root)
	}
	return lead.SubmoduleLookupError(err, work)
}

// OpenNew is Open that never returns the role's live peer: it starts another
// pane, within the same session limits, so that two independent tasks of one
// role run side by side.
func OpenNew(ctx context.Context, role string) (Entry, error) {
	opts, err := callerOptions(ctx)
	if err != nil {
		return Entry{}, err
	}
	opts.NewOnly = true
	return Open(WithOptions(ctx, opts), role)
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
	// Open holds the placement lock here: the right time to drop the model
	// files that peers of older versions left behind.
	// Best effort: a file that stays does no harm, so a failure never stops
	// the open.
	_ = e.sweepEnv()
	_ = e.sweepModels()
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

type peerWatch struct {
	entry       session.Entry
	begin       time.Time
	last        State
	idle, stuck int
	worked      bool
}

func (e *env) watchStep(ctx context.Context, w *peerWatch, startup bool) (Entry, bool, error) {
	entry := w.entry
	if question, err := e.newQuestion(ctx, entry); err != nil || question != "" {
		return Entry{ID: entry.ID, State: Question, Question: question}, true, err
	}
	state, screen, err := e.observe(ctx, entry)
	if err != nil {
		return Entry{ID: entry.ID, State: w.last}, true, err
	}
	w.last = state
	if state != Question && entry.Asked != "" {
		entry, err = e.update(entry.ID, func(x *session.Entry) { x.Asked = "" })
		if err != nil {
			return Entry{ID: w.entry.ID, State: state}, true, err
		}
		w.entry = entry
	}
	switch state {
	case Closed, Exited:
		line, err := e.markEnded(entry, state)
		return Entry{ID: entry.ID, State: state, Note: line}, true, err
	case Question:
		return e.watchQuestion(ctx, w, screen, startup)
	case Limited:
		line, err := e.runHere(entry, entry.Task, screen.Text)
		return Entry{ID: entry.ID, State: Limited, Note: line}, true, err
	case Interrupted:
		return Entry{ID: entry.ID, State: Interrupted, Note: interruptedNote(entry)}, true, nil
	case Ready:
		return e.watchReady(ctx, w, screen, startup)
	default:
		w.idle, w.stuck = 0, 0
		w.worked = w.worked || screen.Working
		return Entry{}, false, nil
	}
}

func (e *env) watchQuestion(ctx context.Context, w *peerWatch, screen Screen, startup bool) (Entry, bool, error) {
	entry := w.entry
	if err := e.ask(entry, screen.Question); err != nil {
		return Entry{ID: entry.ID, State: Question}, true, err
	}
	if e.watchQuestions {
		if err := e.notifyStuck(ctx, entry, time.Now()); err != nil {
			return Entry{ID: entry.ID, State: Question}, true, err
		}
	}
	entry, err := e.find(entry.ID)
	if err != nil {
		return Entry{}, true, err
	}
	w.entry = entry
	if e.watchQuestions && e.cfg.Rex.StuckNotifyMinutes > 0 && !entry.QuestionNotified {
		return Entry{}, false, nil
	}
	return Entry{ID: entry.ID, State: Question, Question: startupQuestion(screen, startup)}, true, nil
}

func (e *env) watchReady(ctx context.Context, w *peerWatch, screen Screen, startup bool) (Entry, bool, error) {
	entry := w.entry
	if startup || entry.Task == "" {
		if screen.Empty {
			w.idle++
		} else {
			w.idle = 0
		}
		return Entry{ID: entry.ID, State: Ready}, w.idle >= 2, nil
	}
	if corescreen.StuckInput(screen, e.peerRoot(entry), entry.Task) {
		w.stuck++
		if w.stuck >= 2 {
			return Entry{ID: entry.ID, State: Ready, Note: ErrStuck.Error()}, true, fmt.Errorf("peer %s: %w", entry.ID, ErrStuck)
		}
		return Entry{}, false, nil
	}
	w.stuck = 0
	if !w.worked && e.opts.Deps.Clock.Now().Sub(w.begin) < e.opts.Deps.Pauses.WorkGrace {
		return Entry{}, false, nil
	}
	w.idle++
	if w.idle < 2 {
		return Entry{}, false, nil
	}
	out, err := e.finishTask(ctx, entry)
	return out, true, err
}

func (e *env) finishTask(ctx context.Context, entry session.Entry) (Entry, error) {
	out := Entry{ID: entry.ID, State: Ready}
	if err := e.credit(entry); err != nil {
		return out, err
	}
	if _, err := e.update(entry.ID, func(x *session.Entry) { x.Task, x.Base = "", nil }); err != nil {
		return out, err
	}
	// The task ends; the pane keeps its slot.
	note, err := e.taskEnded(ctx, entry)
	if err != nil {
		return out, err
	}
	e.attempt(entry, "done", run.CodeNone)
	if note != "" {
		note = "; " + note
	}
	out.Note = "task finished" + note
	return out, nil
}
