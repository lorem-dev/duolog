// Package peer opens and drives a second agent session in a Rex pane. The lead
// answers every question the session asks; duolog presses no key of its own.
package peer

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/delegate"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/models"
	"github.com/lorem-dev/duolog/internal/rex"
	"github.com/lorem-dev/duolog/internal/router"
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

// RunHereError carries the line that tells the lead where to do the task now.
type RunHereError struct{ Line string }

func (e *RunHereError) Error() string { return e.Line }

// Entry is what Open and Wait report. Question is set for a question, and Note
// is one line of context: a timeout, a run here line, or a closed session.
type Entry struct {
	ID       string
	State    State
	Question string
	Note     string
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
}

type optionsKey struct{}

func WithOptions(ctx context.Context, opts Options) context.Context {
	return context.WithValue(ctx, optionsKey{}, opts)
}

type env struct {
	opts   Options
	root   string
	dir    string
	cfg    *config.Config
	reg    *session.Registry
	bus    *bus.Bus
	rex    rex.Client
	leadID string
}

type leadInfo struct {
	id      string
	target  *config.Target
	mode    mode.Mode
	address string
}

func setup(ctx context.Context) (*env, error) {
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
		if cfg, err = config.Load(root); err != nil {
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
	b, err := bus.Open(s.Dir, cfg.Wire)
	if err != nil {
		return nil, err
	}
	leadID := opts.Lead
	if leadID == "" {
		leadID = session.CurrentLead(os.Environ())
	}
	client.Dir = root
	return &env{opts: opts, root: root, dir: s.Dir, cfg: cfg, reg: reg, bus: b, rex: client, leadID: leadID}, nil
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
	change(&entry)
	entry.UpdatedAt = time.Now()
	return entry, e.reg.Record(entry)
}

func newID() string { return "peer-" + strings.ToLower(rand.Text()[:6]) }

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// Open starts a peer for the role, or returns the live one. It records the ID
// right after rex split, then waits until the session is ready, asks a
// question, has ended or the start time is over. The pane is never closed here.
func Open(ctx context.Context, role string) (Entry, error) {
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
	route, err := router.Resolve(e.cfg, role, unavailable, router.Options{Lead: lead.target, AllowManualModel: e.opts.AllowManualModel})
	if err != nil {
		return Entry{}, err
	}
	if route.InLead {
		here := delegate.Placement{Where: "subagent", Model: route.Model, Effort: route.Effort}
		return Entry{State: Limited, Note: strings.TrimSpace(here.String())}, nil
	}
	peers, err := e.livePeers(ctx, lead.id, route.Agent)
	if err != nil {
		return Entry{}, err
	}
	for _, p := range peers {
		if p.Role == role {
			return e.watch(ctx, p, true)
		}
	}
	if limit := e.cfg.Limits.MaxLiveSessions; limit > 0 && len(peers) >= limit {
		return Entry{}, fmt.Errorf("max_live_sessions is %d and %s session %s is still open; close it with duolog peer close %s first", limit, route.Agent, peers[0].ID, peers[0].ID)
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
	argv := launchArgv(runtime.GOOS, exe, agentEnv(e.cfg, route.Agent, lead.id), agentArgv(route, lead.mode))
	client := e.rex
	client.Label = route.Agent + "-" + role
	block, err := client.Split(ctx, argv)
	if err != nil {
		return Entry{}, err
	}
	entry := session.Entry{ID: newID(), Lead: lead.id, Agent: route.Agent, Role: role, Thread: peerThread, Block: block, Mode: lead.mode, UpdatedAt: time.Now()}
	if err := e.reg.RecordLimited(entry, e.cfg.Limits.MaxLiveSessions); err != nil {
		// The pane exists but nothing records it: do not leave it unowned.
		_ = e.rex.Close(ctx, block)
		return Entry{}, fmt.Errorf("pane %s was opened but not recorded and was closed: %w", block, err)
	}
	return e.watch(ctx, entry, true)
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
		alive, err := e.rex.Alive(ctx, x.Block, e.cfg.Rex.CaptureLines)
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
	presence, err := e.rex.Probe(ctx, entry.Block)
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
	return screen.State, screen, err
}

func (e *env) screen(ctx context.Context, entry session.Entry) (Screen, error) {
	raw, err := e.rex.Capture(ctx, entry.Block, e.cfg.Rex.CaptureLines)
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
			return Entry{ID: entry.ID, State: Question, Question: screen.Question}, nil
		case Limited:
			line, err := e.runHere(entry, entry.Task)
			return Entry{ID: entry.ID, State: Limited, Note: line}, err
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
			if stuckInput(screen, entry.Task) {
				if stuck++; stuck >= 2 {
					return Entry{ID: entry.ID, State: Ready, Note: ErrStuck.Error()}, fmt.Errorf("peer %s: %w", entry.ID, ErrStuck)
				}
				break
			}
			stuck = 0
			if worked || time.Since(begin) >= workGrace {
				if idle++; idle >= 2 {
					if _, err := e.update(entry.ID, func(x *session.Entry) { x.Task, x.Base = "", nil }); err != nil {
						return Entry{ID: entry.ID, State: Ready}, err
					}
					return Entry{ID: entry.ID, State: Ready, Note: "task finished"}, nil
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
	return e.watch(ctx, entry, entry.Task == "")
}

// stuckInput reports an idle input line that still holds the sent task line.
func stuckInput(s Screen, taskPath string) bool {
	return s.Composer && !s.Working && s.Input != "" && strings.HasPrefix(s.Input, probe(taskLine(taskPath)))
}

func taskLine(path string) string { return "Read " + path + " and do it. Do not call rex." }

// probe is the start of a line: enough to tell it from a line that lost its start.
func probe(text string) string { return cut(text, 40) }

// ask sends a question to the lead once. Nothing is pressed in the pane.
func (e *env) ask(entry session.Entry, question string) error {
	if question == "" || entry.Asked == question {
		return nil
	}
	text := question
	for {
		_, err := e.bus.Send(bus.Message{From: entry.Agent + "/" + entry.Role, To: e.address(entry.Lead), K: gate.Ask, Ref: entry.ID, S: text})
		if errors.Is(err, bus.ErrTooLong) && len(text) > 20 {
			text = cut(text, len(text)/2)
			continue
		}
		if err != nil {
			return err
		}
		break
	}
	_, err := e.update(entry.ID, func(x *session.Entry) { x.Asked = question })
	return err
}

// markEnded records that the pane is gone or its program has ended. The first
// time, it also puts one status line on the bus. Files are never touched.
func (e *env) markEnded(entry session.Entry, state State) (string, error) {
	line, err := e.endLine(entry, state)
	if entry.Closed {
		return line, err
	}
	if _, uerr := e.update(entry.ID, func(x *session.Entry) { x.Closed = true }); uerr != nil {
		return line, uerr
	}
	msg := bus.Message{From: entry.Agent + "/" + entry.Role, To: e.address(entry.Lead), K: "status", Ref: entry.ID, S: cut(line, e.cfg.Wire.MaxSummaryBytes)}
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

func (e *env) endLine(entry session.Entry, state State) (string, error) {
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
func (e *env) runHere(entry session.Entry, taskPath string) (string, error) {
	lead, err := e.lead(time.Now())
	if err != nil {
		return "", err
	}
	unavailable, err := e.reg.Unavailable()
	if err != nil {
		return "", err
	}
	unavailable[entry.Agent] = true
	route, err := router.Resolve(e.cfg, entry.Role, unavailable, router.Options{Lead: lead.target, AllowManualModel: e.opts.AllowManualModel})
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
func Send(ctx context.Context, id, taskRef string) error {
	e, err := setup(ctx)
	if err != nil {
		return err
	}
	entry, err := e.find(id)
	if err != nil {
		return err
	}
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
	before, err := delegate.Snapshot(e.root)
	if err != nil {
		return err
	}
	text := taskLine(path)
	if err := e.deliver(ctx, entry, text); err != nil {
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

// settle requires the empty prompt on two samples in a row. Anything else is
// reported as a refusal and nothing is typed.
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
			line, err := e.runHere(entry, taskPath)
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
		if state != Ready {
			return fmt.Errorf("peer %s is %s, not ready", entry.ID, state)
		}
		if !s.Empty {
			return fmt.Errorf("peer %s is ready but its input line holds text; nothing was sent", entry.ID)
		}
	}
	return nil
}

// deliver types the line and checks the input line before enter. A line that
// lost its start, as one did after /new, is cleared with ctrl+u and typed again,
// twice at most; the input line is left empty when it still fails.
func (e *env) deliver(ctx context.Context, entry session.Entry, text string) error {
	head := probe(text)
	for try := 1; try <= 3; try++ {
		if err := e.rex.Send(ctx, entry.Block, text); err != nil {
			return err
		}
		if err := sleep(ctx, sendDelay); err != nil {
			return err
		}
		screen, err := e.screen(ctx, entry)
		if err != nil {
			return err
		}
		if screen.Composer && strings.HasPrefix(screen.Input, head) {
			return e.submit(ctx, entry, head)
		}
		if err := e.rex.SendKey(ctx, entry.Block, "ctrl+u"); err != nil {
			return err
		}
		if err := sleep(ctx, keyDelay); err != nil {
			return err
		}
	}
	return fmt.Errorf("peer %s: the text did not reach the input line in 3 tries, so enter was not pressed and the line was cleared", entry.ID)
}

// submit presses enter and looks for the text leaving the input line. A lost
// enter is pressed once more.
func (e *env) submit(ctx context.Context, entry session.Entry, head string) error {
	for press := 1; press <= 2; press++ {
		if err := e.rex.SendKey(ctx, entry.Block, "enter"); err != nil {
			return err
		}
		if err := sleep(ctx, sendDelay); err != nil {
			return err
		}
		screen, err := e.screen(ctx, entry)
		if err != nil {
			return err
		}
		if screen.Working || !screen.Composer || !strings.HasPrefix(screen.Input, head) {
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
		return st, nil
	case Question:
		if err := e.ask(entry, screen.Question); err != nil {
			return st, err
		}
	case Limited:
		if st.Line, err = e.runHere(entry, entry.Task); err != nil {
			return st, err
		}
	}
	changed, known := e.changedFiles(entry)
	st.ChangedFiles = changed
	if st.Line == "" {
		st.Line = fmt.Sprintf("%s %s", entry.ID, state)
		if state == Question {
			st.Line += ": " + screen.Question
		}
		if entry.Task != "" {
			st.Line += ", task " + entry.Task + " is unfinished"
			if known {
				st.Line += fmt.Sprintf(", %d files changed", changed)
			}
		}
	}
	if note := e.restartNote(entry); note != "" {
		st.Line += "; " + note
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
	e, err := setup(ctx)
	if err != nil {
		return err
	}
	choice = strings.TrimSpace(choice)
	if choice == "" {
		return errors.New("answer needs the label or number of an option")
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
	if len(screen.Options) == 0 {
		return fmt.Errorf("peer %s: the question has no options duolog can select; answer it in the pane", entry.ID)
	}
	target, err := pick(screen.Options, choice)
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
		if err := e.rex.SendKey(ctx, entry.Block, key); err != nil {
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
	if err := e.rex.SendKey(ctx, entry.Block, "enter"); err != nil {
		return err
	}
	if err := sleep(ctx, sendDelay); err != nil {
		return err
	}
	// Read the state again: the next question, if any, goes to the lead.
	after, err := e.screen(ctx, entry)
	if err != nil {
		return err
	}
	entry, err = e.update(entry.ID, func(x *session.Entry) { x.Asked = "" })
	if err != nil {
		return err
	}
	if after.State == Question {
		return e.ask(entry, after.Question)
	}
	return nil
}

// pick finds an option by number, exact label, unique prefix or unique part.
func pick(options []Option, choice string) (int, error) {
	if n, err := strconv.Atoi(choice); err == nil {
		for i, o := range options {
			if o.Number == n {
				return i, nil
			}
		}
	}
	want := strings.ToLower(choice)
	for _, match := range []func(label string) bool{
		func(l string) bool { return l == want },
		func(l string) bool { return strings.HasPrefix(l, want) },
		func(l string) bool { return strings.Contains(l, want) },
	} {
		found := -1
		for i, o := range options {
			if match(strings.ToLower(o.Label)) {
				if found >= 0 {
					found = -2
					break
				}
				found = i
			}
		}
		if found >= 0 {
			return found, nil
		}
		if found == -2 {
			return 0, fmt.Errorf("%q matches more than one option", choice)
		}
	}
	var labels []string
	for _, o := range options {
		labels = append(labels, o.Label)
	}
	return 0, fmt.Errorf("no option %q; the options are: %s", choice, strings.Join(labels, " | "))
}

// Close closes the pane and the record. The id may be a pane ID, which is what
// doctor prints. A pane that is already gone is not an error, and files stay.
func Close(ctx context.Context, id string) error {
	e, err := setup(ctx)
	if err != nil {
		return err
	}
	entry, err := e.find(id)
	if err != nil {
		return err
	}
	closeErr := e.rex.Close(ctx, entry.Block)
	if !entry.Closed {
		if _, err := e.update(entry.ID, func(x *session.Entry) { x.Closed = true }); err != nil {
			return errors.Join(closeErr, err)
		}
	}
	return closeErr
}
