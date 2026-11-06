// Package peer opens and drives a second agent session in a Rex pane. The lead
// answers unresolved questions; configured safe answers and command gate
// decisions settle routine prompts without involving the lead.
package peer

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lorem-dev/duolog/internal/project"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gofrs/flock"
	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/models"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/coauthor"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	corescreen "github.com/lorem-dev/duolog/internal/core/screen"
	"github.com/lorem-dev/duolog/internal/delegate"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/rex"
	"github.com/lorem-dev/duolog/internal/runner"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/task"
)

// Thread marks the registry entries of live panes, apart from stored threads.
const peerThread = "duolog-peer"

// Pauses are variables so that tests need not wait. sendDelay is the 0.7 s that
// the recorded run needed between rex send and rex send-key.
var (
	pollInterval = 2 * time.Second
	settleDelay  = time.Second
	sendDelay    = 700 * time.Millisecond
	keyDelay     = 150 * time.Millisecond
	// workGrace is how long a sent task may show an idle prompt before the
	// session counts as finished without having been seen at work.
	workGrace    = 10 * time.Second
	sleep        = pause
	resolveModel = models.Resolve
)

// ShortenPauses sets every pause between pane reads and keys to d, for tests
// of other packages that drive a fake rex, and returns a function that puts
// the pauses back. workGrace stays as it is. The commands never call it.
func ShortenPauses(d time.Duration) (restore func()) {
	poll, settle, send, key := pollInterval, settleDelay, sendDelay, keyDelay
	pollInterval, settleDelay, sendDelay, keyDelay = d, d, d, d
	return func() { pollInterval, settleDelay, sendDelay, keyDelay = poll, settle, send, key }
}

func pause(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ErrStuck means the task text is still in the input line of an idle session.
var ErrStuck = errors.New("task text is stuck in the input line")

// Entry is what Open and Wait report. Question is set for a question, and Note
// is one line of context: a timeout, a run here line, or a closed session.
type Entry struct {
	ID       string
	State    State
	Question string
	Note     string
	// Report is the path of the peer's report file, set only when it exists.
	Report string
}

// reportLine returns the path of the peer's report file when the peer has
// written it, and "" otherwise. The gate lets the peer write the report path
// under the checkout the peer works in, which in a linked worktree is not the
// project state folder, so this looks there too. The path is relative to the
// current checkout when the peer works in it, and absolute otherwise.
func (e *env) reportLine(entry session.Entry) string {
	root := entry.WorkRoot
	if root == "" {
		root = e.root
	}
	path := filepath.Join(root, filepath.FromSlash(gate.ReportPath(entry.ID)))
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	if sameWorkRoot(root, e.root) {
		return gate.ReportPath(entry.ID)
	}
	return path
}

// Report is what Status returns. Go cannot have a type and a function both
// named Status, so the function keeps the name that callers use.
type Report struct {
	State        State
	Task         string
	ChangedFiles int
	Line         string
}

// Options is what the commands cannot take from the environment.
type Options struct {
	Root             string
	Config           *config.Config
	Lead             string
	AllowManualModel bool
	// ProtectedRoots are the telemetry and balance roots the command resolved;
	// they reach a Claude peer's launch settings.
	ProtectedRoots []string
	// Observe receives every open, send and close, failed ones included.
	Observe OperationObserver
	// ObserveAttempt receives each task a peer was sent, once it has ended, as
	// a run placed on the peer.
	ObserveAttempt delegate.Observer
}

// Operation is one open, send or close as the library ran it: its agent, how
// it ended and how long it took. It carries no screen, answer, peer id or path.
// Run is the duolog run id of the peer's launch, the pane's DUOLOG_RUN; it is
// empty when the operation failed before a peer was known.
// Outcome is done, needs (a question), lead (run here), interrupted or failed.
type Operation struct {
	Op, Agent, Run, Outcome string
	Duration                time.Duration
}

// OperationObserver cannot change an operation: it returns nothing, and the
// library neither waits for nor reads what it does.
type OperationObserver func(Operation)

// observeOperation reports an operation that has ended. Agent is empty when
// it failed before the agent was known, and so is run before the peer's launch
// drew one.
func observeOperation(ctx context.Context, op, agent, run string, started time.Time, state State, err error) {
	opts, _ := ctx.Value(optionsKey{}).(Options)
	if opts.Observe == nil {
		return
	}
	opts.Observe(Operation{Op: op, Agent: agent, Run: run, Outcome: corescreen.OperationOutcome(state, err), Duration: time.Since(started)})
}

// attempt reports the task the peer was sent as one run, once it has ended:
// finished, ended with the pane, or closed unfinished. The family and effort
// are the role's, as the lead's route gives them now.
func (e *env) attempt(entry session.Entry, outcome, code string) {
	if e.opts.ObserveAttempt == nil || entry.Task == "" {
		return
	}
	a := delegate.Attempt{Number: 1, Role: entry.Role, Agent: entry.Agent, Run: entry.Run, Placement: "peer", Outcome: outcome, ErrorCode: code, Reason: "none"}
	if code == runner.CodeQuota {
		a.Reason = "quota"
	}
	if lead, err := e.lead(time.Now()); err == nil {
		if route, err := coreroute.Resolve(e.cfg, entry.Role, nil, coreroute.Options{Lead: lead.target, AllowManualModel: e.opts.AllowManualModel}); err == nil && route.Agent == entry.Agent {
			a.Family, a.Effort = coreroute.Normalize(route.Agent, route.Model), route.Effort
		}
	}
	if model, err := os.ReadFile(e.modelPath(entry)); err == nil {
		a.Family = coreroute.Normalize(entry.Agent, string(model))
	}
	if sent := e.sentAt(entry.ID); !sent.IsZero() {
		a.Duration = time.Since(sent)
	}
	e.opts.ObserveAttempt(a)
}

// sentAt is when the lead last sent this peer a task, from the bus.
func (e *env) sentAt(id string) time.Time {
	return taskTimes(e.dir)[id]
}

type optionsKey struct{}

func WithOptions(ctx context.Context, opts Options) context.Context {
	return context.WithValue(ctx, optionsKey{}, opts)
}

type env struct {
	opts           Options
	root           string
	dir            string
	cfg            *config.Config
	reg            *session.Registry
	bus            *bus.Bus
	rex            rex.Client
	leadID         string
	watchQuestions bool
	// gate is the account gate, opened on first use by accounts.
	gate *paidruns.Gate
}

type leadInfo struct {
	id      string
	target  *config.Target
	mode    mode.Mode
	address string
}

func setup(ctx context.Context) (*env, error) { return setupFor(ctx, true) }

func setupFor(ctx context.Context, requireLead bool) (*env, error) {
	e, err := setupRecords(ctx, requireLead)
	if err != nil {
		return nil, err
	}
	if e.bus, err = bus.Open(e.dir, e.cfg.Wire); err != nil {
		return nil, err
	}
	return e, nil
}

// setupViewer is the setup of the passive viewers, show and view. It needs
// no lead and opens no bus, so it creates no bus state and works with a bus
// the caller may only read. e.bus is nil.
func setupViewer(ctx context.Context) (*env, error) { return setupRecords(ctx, false) }

// setupRecords checks the caller and opens the registry and Rex, but not the
// bus.
func setupRecords(ctx context.Context, requireLead bool) (*env, error) {
	if depth, _ := strconv.Atoi(os.Getenv("DUOLOG_DEPTH")); depth >= 1 {
		return nil, fmt.Errorf("workers cannot open or control peer sessions (DUOLOG_DEPTH=%d)", depth)
	}
	opts, _ := ctx.Value(optionsKey{}).(Options)
	if opts.Root == "" {
		return nil, errors.New("peer: missing project root")
	}
	if os.Getenv("REX_SESSION") == "" {
		return nil, errors.New("peer sessions need Rex: REX_SESSION is not set; run this command in a Rex pane")
	}
	client, ok := rex.Detect()
	if !ok {
		return nil, errors.New("peer sessions need Rex: no rex executable in PATH, REX_BIN_DIR or the Rex app")
	}
	root, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, err
	}
	cfg := opts.Config
	if cfg == nil {
		if cfg, err = configfile.Load(root); err != nil {
			return nil, err
		}
	}
	s, err := store.Open(root)
	if err != nil {
		return nil, err
	}
	reg, err := session.Open(s.Dir)
	if err != nil {
		return nil, err
	}
	leadID := opts.Lead
	if requireLead {
		lead, err := reg.ResolveLead(opts.Lead)
		if err != nil {
			return nil, err
		}
		leadID = lead.ID
	}
	root = project.CurrentWorkRoot(root)
	client.Dir = root
	return &env{opts: opts, root: root, dir: s.Dir, cfg: cfg, reg: reg, rex: client, leadID: leadID}, nil
}

// lead reads the lead's live mode and target. A lead that is not registered
// leaves the defaults, as delegate does.
func (e *env) lead(now time.Time) (leadInfo, error) {
	info := leadInfo{mode: mode.Ask, address: "duolog/lead"}
	if e.leadID == "" {
		return info, nil
	}
	_, ok, err := e.reg.Lead(e.leadID)
	if err != nil || !ok {
		return info, err
	}
	lead, m, err := gate.LeadMode(e.reg, e.leadID, now)
	if err != nil {
		return info, err
	}
	if lead.TranscriptPath != "" {
		observed, _ := session.ReadLeadState(lead.Agent, lead.TranscriptPath)
		if observed.Model != "" {
			lead.Model = observed.Model
		}
		if observed.Effort != "" {
			lead.Effort = observed.Effort
		}
	}
	return leadInfo{id: lead.ID, target: &lead.Target, mode: m, address: session.Address(lead)}, nil
}

func (e *env) address(leadID string) string {
	if leadID != "" {
		if lead, ok, _ := e.reg.Lead(leadID); ok {
			return session.Address(lead)
		}
	}
	return "duolog/lead"
}

// find matches a session by its ID or by its pane, which is what doctor names.
func (e *env) find(key string) (session.Entry, error) {
	all, err := e.reg.Entries()
	if err != nil {
		return session.Entry{}, err
	}
	for i := len(all) - 1; i >= 0; i-- {
		if x := all[i]; x.Block != "" && (x.ID == key || x.Block == key) {
			return x, nil
		}
	}
	return session.Entry{}, fmt.Errorf("unknown peer session %q", key)
}

func (e *env) update(id string, change func(*session.Entry)) (session.Entry, error) {
	entry, err := e.find(id)
	if err != nil {
		return entry, err
	}
	return e.reg.Update(entry.ID, change)
}

func newID() string { return "peer-" + strings.ToLower(rand.Text()[:6]) }

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

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
	opening := flock.New(filepath.Join(e.dir, "peer-open.lock"))
	if err := opening.Lock(); err != nil {
		return Entry{}, err
	}
	defer opening.Close()
	peers, err := e.livePeers(ctx, lead.id, route.Agent)
	if err != nil {
		return Entry{}, err
	}
	for _, p := range peers {
		if p.Role == role && sameWorkRoot(p.WorkRoot, e.root) {
			run = p.Run
			if err := opening.Unlock(); err != nil {
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
	model, err := resolveModel(ctx, e.cfg, filepath.Join(e.dir, "cache"), route.Target)
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
	openedEnvFile(envFile)
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
		attachErr = recordPeer(e.reg, entry, e.cfg.Limits.MaxLiveSessions)
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
	if err := opening.Unlock(); err != nil {
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

// observe reads the pane once: is it there, did the program end, and what does
// the bounded screen show.
func (e *env) observe(ctx context.Context, entry session.Entry) (State, Screen, error) {
	if err := e.credit(entry); err != nil {
		return Starting, Screen{}, err
	}
	presence, err := e.clientFor(entry).Probe(ctx, entry.Block)
	if err != nil {
		return Starting, Screen{}, err
	}
	switch presence {
	case rex.Gone:
		return Closed, Screen{}, nil
	case rex.Exited:
		return Exited, Screen{}, nil
	}
	screen, err := e.screen(ctx, entry)
	if rex.IsNotFound(err) {
		return Closed, Screen{}, nil
	}
	if err != nil {
		return screen.State, screen, err
	}
	queuedOpened := false
	for attempts := 0; screen.State == Question; attempts++ {
		if screen.Queued {
			if attempts >= 16 {
				return Question, screen, fmt.Errorf("peer %s: queued question did not open", entry.ID)
			}
			if !queuedOpened {
				if err := e.clientFor(entry).SendKey(ctx, entry.Block, "shift+left"); err != nil {
					return Question, screen, err
				}
				queuedOpened = true
			}
			if err := sleep(ctx, keyDelay); err != nil {
				return Question, screen, err
			}
			screen, err = e.screen(ctx, entry)
			if err != nil {
				return Starting, screen, err
			}
			continue
		}
		if attempts >= 16 {
			return Question, screen, fmt.Errorf("peer %s: automatic answers did not clear the question", entry.ID)
		}
		queuedOpened = false
		handled, err := e.autoAnswer(ctx, entry, screen)
		if err != nil {
			return Question, screen, err
		}
		if !handled {
			break
		}
		screen, err = e.screen(ctx, entry)
		if err != nil {
			return Starting, screen, err
		}
	}
	return screen.State, screen, nil
}

// screen keeps the capture's styles, which tell a dim suggestion in the input
// line from typed text.
func (e *env) screen(ctx context.Context, entry session.Entry) (Screen, error) {
	raw, err := e.clientFor(entry).CaptureStyled(ctx, entry.Block, e.cfg.Rex.CaptureLines)
	if err != nil {
		return Screen{}, err
	}
	return Parse(entry.Agent, raw), nil
}

// watch waits on a session. At startup it stops at the first of: ready on two
// samples in a row, a question, a limit, the end of the program, or
// start_timeout_seconds. With a task sent it waits for the session to be idle
// again, and reports text that is stuck in the input line.
func (e *env) watch(ctx context.Context, entry session.Entry, startup bool) (Entry, error) {
	begin := time.Now()
	deadline := begin.Add(time.Duration(e.cfg.Rex.StartTimeoutSeconds) * time.Second)
	last := Starting
	var idle, stuck int
	worked := false
	for {
		if question, err := pendingAsk(e.dir, e.address(e.leadID), entry.ID); err != nil {
			return Entry{}, err
		} else if question != "" {
			return Entry{ID: entry.ID, State: Question, Question: question}, nil
		}
		state, screen, err := e.observe(ctx, entry)
		if err != nil {
			return Entry{ID: entry.ID, State: last}, err
		}
		last = state
		if state != Question && entry.Asked != "" {
			entry, err = e.update(entry.ID, func(x *session.Entry) { x.Asked = "" })
			if err != nil {
				return Entry{ID: entry.ID, State: last}, err
			}
		}
		switch state {
		case Closed, Exited:
			line, err := e.markEnded(entry, state)
			return Entry{ID: entry.ID, State: state, Note: line}, err
		case Question:
			if err := e.ask(entry, screen.Question); err != nil {
				return Entry{ID: entry.ID, State: state}, err
			}
			if e.watchQuestions {
				if err := e.notifyStuck(ctx, entry, time.Now()); err != nil {
					return Entry{ID: entry.ID, State: state}, err
				}
			}
			entry, err = e.find(entry.ID)
			if err != nil {
				return Entry{}, err
			}
			if e.watchQuestions && e.cfg.Rex.StuckNotifyMinutes > 0 && !entry.QuestionNotified {
				break
			}
			return Entry{ID: entry.ID, State: Question, Question: screen.Question}, nil
		case Limited:
			line, err := e.runHere(entry, entry.Task, screen.Text)
			return Entry{ID: entry.ID, State: Limited, Note: line}, err
		case Interrupted:
			return Entry{ID: entry.ID, State: Interrupted, Note: interruptedNote(entry)}, nil
		case Ready:
			if startup || entry.Task == "" {
				if screen.Empty {
					idle++
				} else {
					idle = 0
				}
				if idle >= 2 {
					return Entry{ID: entry.ID, State: Ready}, nil
				}
				break
			}
			if corescreen.StuckInput(screen, e.root, entry.Task) {
				if stuck++; stuck >= 2 {
					return Entry{ID: entry.ID, State: Ready, Note: ErrStuck.Error()}, fmt.Errorf("peer %s: %w", entry.ID, ErrStuck)
				}
				break
			}
			stuck = 0
			if worked || time.Since(begin) >= workGrace {
				if idle++; idle >= 2 {
					if err := e.credit(entry); err != nil {
						return Entry{ID: entry.ID, State: Ready}, err
					}
					if _, err := e.update(entry.ID, func(x *session.Entry) { x.Task, x.Base = "", nil }); err != nil {
						return Entry{ID: entry.ID, State: Ready}, err
					}
					// The task ends; the pane keeps its slot.
					note, err := e.taskEnded(ctx, entry)
					if err != nil {
						return Entry{ID: entry.ID, State: Ready}, err
					}
					e.attempt(entry, "done", runner.CodeNone)
					if note != "" {
						note = "; " + note
					}
					return Entry{ID: entry.ID, State: Ready, Note: "task finished" + note}, nil
				}
			}
		default:
			idle, stuck = 0, 0
			worked = worked || screen.Working
		}
		if ctx.Err() != nil {
			return Entry{ID: entry.ID, State: last}, ctx.Err()
		}
		if startup && time.Now().After(deadline) {
			note := fmt.Sprintf("timed out after %ds in state %s; the pane stays open", e.cfg.Rex.StartTimeoutSeconds, last)
			return Entry{ID: entry.ID, State: last, Note: note}, nil
		}
		if err := sleep(ctx, pollInterval); err != nil {
			return Entry{ID: entry.ID, State: last}, err
		}
	}
}

// Wait continues the wait for a session, for example after an answer.
func Wait(ctx context.Context, id string) (Entry, error) {
	e, err := setup(ctx)
	if err != nil {
		return Entry{}, err
	}
	entry, err := e.find(id)
	if err != nil {
		return Entry{}, err
	}
	if entry.Closed {
		line, err := e.endLine(entry, Closed)
		return Entry{ID: entry.ID, State: Closed, Note: line}, err
	}
	e.watchQuestions = true
	got, err := e.watchMarked(ctx, entry)
	if got.ID != "" {
		got.Report = e.reportLine(entry)
	}
	return got, err
}

// interruptedNote tells the lead that the peer stopped, for example after a
// denied permission prompt, and waits for a follow-up.
func interruptedNote(entry session.Entry) string {
	note := "it stopped and waits for what to do instead"
	if entry.Task != "" {
		note += "; task " + entry.Task + " is unfinished"
	}
	return note + "; send a follow-up with duolog peer send " + entry.ID + " <task>"
}

// holds reports an input line that holds the text and nothing else, read across
// the rows it wraps over. Claude's rows end with its box, so any row more is
// input. Codex has no box: the first row that does not continue the text after
// all of it was read is taken for its footer.
func holds(agent string, s Screen, text string) bool {
	if !s.Composer {
		return false
	}
	want, got := corescreen.Squash(text), ""
	for _, row := range s.Rows {
		next := got + corescreen.Squash(row)
		if !strings.HasPrefix(want, next) {
			return agent == "codex" && got == want
		}
		got = next
	}
	return got == want
}

// ask sends a question to the lead once. Nothing is pressed in the pane.
func (e *env) ask(entry session.Entry, question string) error {
	if question == "" {
		return nil
	}
	if entry.Asked == question {
		if entry.QuestionSince.IsZero() {
			_, err := e.update(entry.ID, func(x *session.Entry) {
				if x.Asked == question && x.QuestionSince.IsZero() {
					x.QuestionSince = time.Now()
				}
			})
			return err
		}
		return nil
	}
	text := question
	for {
		_, err := e.bus.Send(bus.Message{From: entry.Agent + "/" + entry.Role, To: e.address(entry.Lead), K: gate.Ask, Ref: entry.ID, S: text})
		if errors.Is(err, bus.ErrTooLong) && len(text) > 20 {
			text = corescreen.Cut(text, len(text)/2)
			continue
		}
		if err != nil {
			return err
		}
		break
	}
	_, err := e.update(entry.ID, func(x *session.Entry) {
		if x.Asked != question {
			x.Asked = question
			x.QuestionSince = time.Now()
			x.QuestionNotified = false
		}
	})
	return err
}

// markEnded records that the pane is gone or its program has ended. The first
// time, it also puts one status line on the bus. Files are never touched.
func (e *env) markEnded(entry session.Entry, state State) (string, error) {
	line, err := e.endLine(entry, state)
	if entry.Closed {
		return line, err
	}
	if _, uerr := e.update(entry.ID, func(x *session.Entry) { x.Closed, x.TaskGeneration = true, "" }); uerr != nil {
		return line, uerr
	}
	// The pane is gone or its program ended: its slot frees, and a task sent
	// to it that did not settle counts at least its estimate when paid, once.
	if serr := e.sessionEnded(context.Background(), entry); serr != nil {
		return line, errors.Join(err, serr)
	}
	// The pane ended under the task: that run failed, and only this first time.
	e.attempt(entry, "failed", runner.CodeExit)
	msg := bus.Message{From: entry.Agent + "/" + entry.Role, To: e.address(entry.Lead), K: "status", Ref: entry.ID, S: corescreen.Cut(line, e.cfg.Wire.MaxSummaryBytes)}
	if _, serr := e.bus.Send(msg); serr != nil {
		return line, errors.Join(err, serr)
	}
	return line, err
}

// changedFiles counts the files that differ from the tree before the task.
func (e *env) changedFiles(entry session.Entry) (int, bool) {
	if entry.Base == nil {
		return 0, false
	}
	now, err := delegate.Snapshot(e.root)
	if err != nil || now == nil {
		return 0, false
	}
	return len(delegate.Changes(entry.Base, now)), true
}

func (e *env) modelPath(entry session.Entry) string {
	return filepath.Join(e.dir, "coauthor-"+entry.ID+".model")
}

func (e *env) credit(entry session.Entry) error {
	if !e.cfg.Coauthors.Enabled {
		return nil
	}
	n, ok := e.changedFiles(entry)
	if !ok || n == 0 {
		return nil
	}
	model, err := os.ReadFile(e.modelPath(entry))
	// Panes opened before co-author recording have no saved model.
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return coauthor.Append(e.dir, e.cfg.Coauthors, coauthor.Record{Lead: entry.Lead, Agent: entry.Agent, Model: string(model), Files: n, Peer: entry.ID})
}

func (e *env) endLine(entry session.Entry, state State) (string, error) {
	if err := e.credit(entry); err != nil {
		return "", err
	}
	line := fmt.Sprintf("%s %s", entry.ID, state)
	if entry.Task != "" {
		line += ": task " + entry.Task + " is unfinished"
		if n, ok := e.changedFiles(entry); ok {
			line += fmt.Sprintf(", %d files changed since it was sent; they are left as they are", n)
		}
	}
	return line, nil
}

// runHere turns a provider limit into the line the lead acts on. The pane stays
// open and nothing is sent or opened.
func (e *env) runHere(entry session.Entry, taskPath, screenText string) (string, error) {
	lead, err := e.lead(time.Now())
	if err != nil {
		return "", err
	}
	unavailable, err := e.reg.Unavailable()
	if err != nil {
		return "", err
	}
	if !unavailable[entry.Agent] {
		// A zero time ends with the lead when the screen names no reset.
		if err := e.reg.MarkUnavailable(entry.Agent, runner.ResetTime(screenText, time.Now())); err != nil {
			return "", err
		}
		unavailable[entry.Agent] = true
	}
	route, err := coreroute.Resolve(e.cfg, entry.Role, unavailable, coreroute.Options{Lead: lead.target, AllowManualModel: e.opts.AllowManualModel})
	if err != nil {
		return "", err
	}
	placement, err := delegate.Place(e.cfg, taskPath, route)
	if err != nil {
		placement = delegate.Placement{Where: "subagent", Model: route.Model, Effort: route.Effort, Task: taskPath}
	}
	return strings.TrimSpace(placement.String()), nil
}

// Send passes one line, a reference to the task file, to a ready session.
func Send(ctx context.Context, id, taskRef string) (err error) {
	started, agent, run := time.Now(), "", ""
	defer func() { observeOperation(ctx, "send", agent, run, started, Ready, err) }()
	e, err := setup(ctx)
	if err != nil {
		return err
	}
	entry, err := e.find(id)
	if err != nil {
		return err
	}
	agent, run = entry.Agent, entry.Run
	ref := strings.TrimSpace(taskRef)
	if strings.HasPrefix(ref, "/") {
		if info, err := os.Stat(ref); err != nil || !info.Mode().IsRegular() {
			return errors.New("slash commands are never sent to a peer: /model and /effort change the user's settings")
		}
	}
	path, err := task.Resolve(e.root, ref)
	if err != nil {
		return err
	}
	if err := task.Validate(path); err != nil {
		return err
	}
	if strings.ContainsAny(path, "\r\n") {
		return errors.New("task path holds a line break")
	}
	if entry.Closed {
		return fmt.Errorf("peer %s is closed; open a new one with duolog peer open", entry.ID)
	}
	if err := e.settle(ctx, entry, path); err != nil {
		return err
	}
	// The pane is idle: a task sent before has ended. It settles before the
	// next is admitted.
	if _, err := e.taskEnded(ctx, entry); err != nil {
		return err
	}
	before, err := delegate.Snapshot(e.root)
	if err != nil {
		return err
	}
	// The task is admitted to the pane's account session and its generation
	// recorded before anything is typed.
	task, err := e.admitSend(ctx, entry)
	if err != nil {
		return err
	}
	if entry, err = e.update(entry.ID, func(x *session.Entry) { x.TaskGeneration, x.AdmissionID = task.id(), task.id() }); err != nil {
		return errors.Join(err, task.release(ctx))
	}
	if err := task.launch(ctx, ""); err != nil {
		return errors.Join(err, e.unsent(ctx, entry, task))
	}
	text := corescreen.TaskLine(e.root, path)
	if err := e.deliver(ctx, entry, text); err != nil {
		if errors.Is(err, errNotDelivered) {
			// Enter was never pressed: the task is proven unsent.
			return errors.Join(err, e.unsent(ctx, entry, task))
		}
		// The task may have reached the agent: it stays held.
		return errors.Join(err, task.attach(ctx, entry.Block, entry.RexSession))
	}
	if err := task.attach(ctx, entry.Block, entry.RexSession); err != nil {
		return err
	}
	entry, err = e.update(entry.ID, func(x *session.Entry) { x.Task, x.Base, x.Asked = path, before, "" })
	if err != nil {
		return err
	}
	lead, err := e.lead(time.Now())
	if err != nil {
		return err
	}
	_, err = e.bus.Send(bus.Message{From: lead.address, To: entry.Agent + "/" + entry.Role, K: "task", Ref: entry.ID, S: "role " + entry.Role + " task=" + filepath.ToSlash(path)})
	return err
}

// settle requires the empty prompt on two samples in a row: ready, or
// interrupted and waiting for a follow-up. A prompt that shows only the
// agent's suggestion is empty, and typing replaces the suggestion. Anything
// else is reported as a refusal and nothing is typed.
func (e *env) settle(ctx context.Context, entry session.Entry, taskPath string) error {
	for sample := 0; sample < 2; sample++ {
		if sample > 0 {
			if err := sleep(ctx, settleDelay); err != nil {
				return err
			}
		}
		state, s, err := e.observe(ctx, entry)
		if err != nil {
			return err
		}
		switch state {
		case Closed, Exited:
			line, err := e.markEnded(entry, state)
			if err != nil {
				return err
			}
			if state == Exited {
				return fmt.Errorf("%s (the program has exited; text sent to it is lost)", line)
			}
			return fmt.Errorf("%s (the pane is closed)", line)
		case Limited:
			line, err := e.runHere(entry, taskPath, s.Text)
			if err != nil {
				return err
			}
			return &RunHereError{Line: line}
		case Question:
			if err := e.ask(entry, s.Question); err != nil {
				return err
			}
			return fmt.Errorf("peer %s has a question; the lead answers it with duolog peer answer", entry.ID)
		}
		if state != Ready && state != Interrupted {
			return fmt.Errorf("peer %s is %s, not ready", entry.ID, state)
		}
		if !s.Empty {
			return fmt.Errorf("peer %s is %s but its input line holds text; nothing was sent", entry.ID, state)
		}
	}
	return nil
}

// errNotDelivered marks a send that failed before enter was pressed: the
// agent never got the task.
var errNotDelivered = errors.New("the task was not delivered")

// undelivered is a failure before enter, worded as the failure itself.
type undelivered struct{ err error }

func (u undelivered) Error() string   { return u.err.Error() }
func (u undelivered) Unwrap() []error { return []error{errNotDelivered, u.err} }

// unsent gives back the hold of a task proven never delivered and takes its
// generation off the entry.
func (e *env) unsent(ctx context.Context, entry session.Entry, task *hold) error {
	_, err := e.reg.Update(entry.ID, func(x *session.Entry) {
		if x.TaskGeneration == task.id() {
			x.TaskGeneration = ""
		}
	})
	return errors.Join(err, task.release(ctx))
}

// deliver types the line and checks the input line before enter. An input line
// that holds anything but the line, such as a line that lost its start after
// /new or one typed twice, is cleared and the line typed again, twice at most;
// the input line is left empty when it still fails. Every failure before
// enter is errNotDelivered; one after enter may have reached the agent.
func (e *env) deliver(ctx context.Context, entry session.Entry, text string) error {
	notDelivered := func(err error) error { return undelivered{err} }
	for try := 1; try <= 3; try++ {
		if err := e.clientFor(entry).Send(ctx, entry.Block, text); err != nil {
			return notDelivered(err)
		}
		if err := sleep(ctx, sendDelay); err != nil {
			return notDelivered(err)
		}
		screen, err := e.screen(ctx, entry)
		if err != nil {
			return notDelivered(err)
		}
		if holds(entry.Agent, screen, text) {
			return e.submit(ctx, entry, text)
		}
		if err := e.clear(ctx, entry, screen); err != nil {
			return notDelivered(err)
		}
	}
	return notDelivered(fmt.Errorf("peer %s: the text did not reach the input line in 3 tries, so enter was not pressed and the line was cleared", entry.ID))
}

// clear empties an input line that holds text with ctrl+c, as a person does:
// ctrl+u clears only one row of an input that wraps. An empty input line is
// left alone, since ctrl+c there arms the agent's exit. Text that is still
// there afterwards is an error, so nothing is typed over it.
func (e *env) clear(ctx context.Context, entry session.Entry, screen Screen) error {
	if !screen.Composer || screen.Empty {
		return nil
	}
	if err := e.clientFor(entry).SendKey(ctx, entry.Block, "ctrl+c"); err != nil {
		return err
	}
	if err := sleep(ctx, keyDelay); err != nil {
		return err
	}
	after, err := e.screen(ctx, entry)
	if err != nil {
		return err
	}
	if after.Composer && !after.Empty {
		return fmt.Errorf("peer %s: ctrl+c did not clear the input line, so enter was not pressed and nothing more was typed", entry.ID)
	}
	return nil
}

// submit presses enter and looks for the text leaving the input line. A lost
// enter is pressed once more.
func (e *env) submit(ctx context.Context, entry session.Entry, text string) error {
	for press := 1; press <= 2; press++ {
		if err := e.clientFor(entry).SendKey(ctx, entry.Block, "enter"); err != nil {
			return err
		}
		if err := sleep(ctx, sendDelay); err != nil {
			return err
		}
		screen, err := e.screen(ctx, entry)
		if err != nil {
			return err
		}
		if screen.Working || !holds(entry.Agent, screen, text) {
			return nil
		}
	}
	return fmt.Errorf("peer %s: enter did not submit the task: %w", entry.ID, ErrStuck)
}

// Status reports one line: state, unfinished task, changed files and whether
// the session must be restarted to match the lead's mode.
func Status(ctx context.Context, id string) (Report, error) {
	e, err := setup(ctx)
	if err != nil {
		return Report{}, err
	}
	entry, err := e.find(id)
	if err != nil {
		return Report{}, err
	}
	state := Closed
	var screen Screen
	if !entry.Closed {
		if state, screen, err = e.observe(ctx, entry); err != nil {
			return Report{}, err
		}
	}
	st := Report{State: state, Task: entry.Task}
	switch state {
	case Closed, Exited:
		if st.Line, err = e.markEnded(entry, state); err != nil {
			return st, err
		}
		st.ChangedFiles, _ = e.changedFiles(entry)
		if path := e.reportLine(entry); path != "" {
			st.Line += "; report " + path
		}
		return st, nil
	case Question:
		if err := e.ask(entry, screen.Question); err != nil {
			return st, err
		}
	case Limited:
		if st.Line, err = e.runHere(entry, entry.Task, screen.Text); err != nil {
			return st, err
		}
	case Ready:
		// A finished task whose settlement did not happen yet settles now,
		// once; a task still recorded as unfinished stays held.
		if entry.Task == "" && entry.TaskGeneration != "" {
			if _, err := e.taskEnded(ctx, entry); err != nil {
				return st, err
			}
		}
	}
	changed, known := e.changedFiles(entry)
	st.ChangedFiles = changed
	if state == Interrupted {
		st.Line = fmt.Sprintf("%s %s: %s", entry.ID, state, interruptedNote(entry))
		if known && entry.Task != "" {
			st.Line += fmt.Sprintf("; %d files changed", changed)
		}
	}
	if st.Line == "" {
		st.Line = fmt.Sprintf("%s %s", entry.ID, state)
		if state == Question {
			st.Line += ": " + screen.Question
		}
		if entry.Unfinished() {
			st.Line += ", task " + entry.Task + " is unfinished"
			if known {
				st.Line += fmt.Sprintf(", %d files changed", changed)
			}
		}
	}
	if note := e.restartNote(entry); note != "" {
		st.Line += "; " + note
	}
	if path := e.reportLine(entry); path != "" {
		st.Line += "; report " + path
	}
	return st, nil
}

// restartNote says so when the flags the session started with are not the
// flags the lead's mode gives now. A pane cannot change its own sandbox.
func (e *env) restartNote(entry session.Entry) string {
	if entry.Lead == "" {
		return ""
	}
	lead, err := e.lead(time.Now())
	if err != nil || lead.id == "" {
		return ""
	}
	sandbox := e.cfg.Roles[entry.Role].Sandbox
	if slices.Equal(mode.Args(entry.Agent, entry.Mode, sandbox), mode.Args(entry.Agent, lead.mode, sandbox)) {
		return ""
	}
	return fmt.Sprintf("restart needed: it started in mode %s and the lead is now in mode %s; run duolog peer close %s, then duolog peer open %s", entry.Mode, lead.mode, entry.ID, entry.Role)
}

// Answer picks an option of the open question by label or number. It moves the
// cursor with navigation keys, checks that it arrived, and only then presses
// enter. A bare enter is never sent, because a default can be "No, exit".
func Answer(ctx context.Context, id, choice string) error {
	return AnswerWithReason(ctx, id, choice, "")
}

// interruptSamples bounds how many screens AnswerWithReason reads while the
// pane turns from the question to its interrupted input line.
const interruptSamples = 5

// AnswerWithReason is Answer that then replies with a reason, one line. A
// Claude permission prompt answered No leaves the pane interrupted, asking
// what to do instead; the reason is typed there after reasonPrefix and
// submitted, as a person does, so the peer goes on with it. Only an interrupted pane with an empty
// input line gets the reason. Any other state is an error that says the
// reason was not sent; the option stays selected.
func AnswerWithReason(ctx context.Context, id, choice, reason string) error {
	e, err := setup(ctx)
	if err != nil {
		return err
	}
	choice = strings.TrimSpace(choice)
	if choice == "" {
		return errors.New("answer needs the label or number of an option")
	}
	reason = strings.TrimSpace(reason)
	if strings.ContainsAny(reason, "\r\n") {
		return errors.New("the reason must be one line")
	}
	if strings.ContainsFunc(reason, unicode.IsControl) {
		return errors.New("the reason cannot hold control characters")
	}
	entry, err := e.find(id)
	if err != nil {
		return err
	}
	state, screen, err := e.observe(ctx, entry)
	if err != nil {
		return err
	}
	if state != Question {
		return fmt.Errorf("peer %s has no question to answer (state %s)", entry.ID, state)
	}
	if err := e.selectOption(ctx, entry, screen, choice); err != nil {
		return err
	}
	// Read the state again: the next question, if any, goes to the lead.
	state, after, err := e.observe(ctx, entry)
	if err != nil {
		return err
	}
	entry, err = e.update(entry.ID, func(x *session.Entry) { x.Asked = "" })
	if err != nil {
		return err
	}
	if reason == "" {
		// The answered question may still show while the pane redraws; only
		// a different one goes to the lead.
		if after.State == Question && after.Question != screen.Question {
			return e.ask(entry, after.Question)
		}
		return nil
	}
	return e.reply(ctx, entry, screen.Question, state, after, reason)
}

// reasonPrefix starts every typed reason with words, so the input line never
// reads it as a mode switch: a leading !, #, $, @ or / is a shell command,
// memory, mention or slash command to Claude Code and Codex, which no gate
// sees.
const reasonPrefix = "Reason: "

// reply types the reason into an interrupted pane once its input line is
// empty. The pane may take a moment to leave the question it answered, so
// that question may stay on screen for the bounded wait; a different question
// goes to the lead at once.
func (e *env) reply(ctx context.Context, entry session.Entry, answered string, state State, screen Screen, reason string) error {
	for sample := 1; ; sample++ {
		switch {
		case state == Question && screen.Question == answered && sample < interruptSamples:
		case state == Question && screen.Question == answered:
			return fmt.Errorf("peer %s still shows the question it was asked, so the reason was not sent: %s", entry.ID, screen.Question)
		case state == Question:
			if err := e.ask(entry, screen.Question); err != nil {
				return err
			}
			return fmt.Errorf("peer %s asks another question, so the reason was not sent: %s", entry.ID, screen.Question)
		case state == Interrupted && screen.Empty:
			return e.deliver(ctx, entry, reasonPrefix+reason)
		case state == Interrupted:
			return fmt.Errorf("peer %s is interrupted but its input line holds text, so the reason was not sent", entry.ID)
		case state == Closed, state == Exited, sample >= interruptSamples:
			return fmt.Errorf("peer %s is %s, not interrupted, so the reason was not sent; send it as a task with duolog peer send", entry.ID, state)
		}
		if err := sleep(ctx, settleDelay); err != nil {
			return err
		}
		var err error
		if state, screen, err = e.observe(ctx, entry); err != nil {
			return err
		}
	}
}

// selectOption is shared by lead answers and automatic answers.
func (e *env) selectOption(ctx context.Context, entry session.Entry, screen Screen, choice string) error {
	if len(screen.Options) == 0 {
		return fmt.Errorf("peer %s: the question has no options duolog can select; answer it in the pane", entry.ID)
	}
	target, err := corescreen.Pick(screen.Options, choice)
	if err != nil {
		return fmt.Errorf("peer %s: %w", entry.ID, err)
	}
	current := slices.IndexFunc(screen.Options, func(o Option) bool { return o.Selected })
	if current < 0 {
		return fmt.Errorf("peer %s: cannot tell which option is selected; answer it in the pane", entry.ID)
	}
	key, steps := "down", target-current
	if steps < 0 {
		key, steps = "up", -steps
	}
	for i := 0; i < steps; i++ {
		if err := e.clientFor(entry).SendKey(ctx, entry.Block, key); err != nil {
			return err
		}
		if err := sleep(ctx, keyDelay); err != nil {
			return err
		}
	}
	if steps > 0 {
		moved, err := e.screen(ctx, entry)
		if err != nil {
			return err
		}
		at := slices.IndexFunc(moved.Options, func(o Option) bool { return o.Selected })
		if moved.State != Question || at < 0 || moved.Options[at].Label != screen.Options[target].Label {
			return fmt.Errorf("peer %s: the cursor did not reach %q, so enter was not pressed", entry.ID, screen.Options[target].Label)
		}
	}
	if err := e.clientFor(entry).SendKey(ctx, entry.Block, "enter"); err != nil {
		return err
	}
	if err := sleep(ctx, sendDelay); err != nil {
		return err
	}
	return nil
}

// pendingAsk never advances the delivery cursor; the lead hook still receives
// the request. Replies are matched by ref so answered requests do not stop wait.
// Only an ask whose ref names this peer stops its wait. An ask that names no
// peer, from a worker outside a pane or from a pane opened before asks named
// their peer, belongs to no peer's wait; the lead's hook still delivers it.
func pendingAsk(dir, address, peer string) (string, error) {
	f, err := os.Open(filepath.Join(dir, "bus.ndjson"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	pending := map[string]string{}
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		var m bus.Message
		if err := json.Unmarshal(line, &m); err != nil {
			return "", err
		}
		if m.K == "ask" && m.To == address && strings.HasPrefix(m.From, "duolog/ask.") && m.Ref == peer && !gate.AskExpired(m) {
			pending[strconv.FormatInt(m.N, 10)] = fmt.Sprintf("ask %d: %s", m.N, m.S)
			if m.P {
				pending[strconv.FormatInt(m.N, 10)] = fmt.Sprintf("ask %d (needs the person): %s", m.N, m.S)
			}
		}
		if (m.K == "allow" || m.K == "deny") && m.From == address {
			delete(pending, m.Ref)
		}
	}
	for _, question := range pending {
		return question, nil
	}
	return "", nil
}

// WindowsNotice explains the supported alternative without invoking Rex.
const WindowsNotice = "Live sessions are not available on Windows; use duolog delegate."

// sameWorkRoot handles filesystem aliases without reusing legacy peers whose
// working directory was never recorded.
func sameWorkRoot(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	left, le := os.Stat(a)
	right, re := os.Stat(b)
	return le == nil && re == nil && os.SameFile(left, right)
}
