package peer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/coauthor"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/run"
	corescreen "github.com/lorem-dev/duolog/internal/core/screen"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/rex"
	"github.com/lorem-dev/duolog/internal/store"
)

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

// attempt reports the task the peer was sent as one run, once it has ended:
// finished, ended with the pane, or closed unfinished. The family and effort
// are the role's, as the lead's route gives them now.
func (e *env) attempt(entry session.Entry, outcome, code string) {
	if e.opts.ObserveAttempt == nil || entry.Task == "" {
		return
	}
	a := delegate.Attempt{Number: 1, Role: entry.Role, Agent: entry.Agent, Run: entry.Run, Placement: "peer", Outcome: outcome, ErrorCode: code, Reason: "none"}
	if code == run.CodeQuota {
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
			if err := e.opts.Deps.Clock.Sleep(ctx, e.opts.Deps.Pauses.Key); err != nil {
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
	return corescreen.Parse(raw, e.pane(entry.Agent).Facts), nil
}

// pane is the pane support of the named agent; an agent that has none reads
// every screen with no texts of its own.
func (e *env) pane(agent string) Pane { return e.opts.Deps.Panes[agent] }

// watch waits on a session. At startup it stops at the first of: ready on two
// samples in a row, a question, a limit, the end of the program, or
// start_timeout_seconds. With a task sent it waits for the session to be idle
// again, and reports text that is stuck in the input line.
func (e *env) watch(ctx context.Context, entry session.Entry, startup bool) (Entry, error) {
	begin := e.opts.Deps.Clock.Now()
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
			if worked || e.opts.Deps.Clock.Now().Sub(begin) >= e.opts.Deps.Pauses.WorkGrace {
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
					e.attempt(entry, "done", run.CodeNone)
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
		if err := e.opts.Deps.Clock.Sleep(ctx, e.opts.Deps.Pauses.Poll); err != nil {
			return Entry{ID: entry.ID, State: last}, err
		}
	}
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
	e.attempt(entry, "failed", run.CodeExit)
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
		if err := e.reg.MarkUnavailable(entry.Agent, e.opts.Deps.ResetTime(entry.Agent, screenText, time.Now())); err != nil {
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
		if err := e.opts.Deps.Clock.Sleep(ctx, e.opts.Deps.Pauses.Key); err != nil {
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
	if err := e.opts.Deps.Clock.Sleep(ctx, e.opts.Deps.Pauses.Send); err != nil {
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
