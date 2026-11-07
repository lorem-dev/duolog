package peer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/bus"
	corescreen "github.com/lorem-dev/duolog/internal/core/screen"
	"github.com/lorem-dev/duolog/internal/core/taskspec"
	"github.com/lorem-dev/duolog/internal/session"
)

// taskFile confines the task file ref to the work root and checks its
// sections. It returns the file's absolute path, which the pane is sent.
func taskFile(root, ref string) (string, error) {
	work := delegate.WorkTree(root)
	name, path, err := work.Resolve(ref)
	if err != nil {
		return "", err
	}
	if err := taskspec.Validate(work.FS, name); err != nil {
		return "", err
	}
	return path, nil
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
	path, err := taskFile(e.root, ref)
	if err != nil {
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
	// The peer's turns count again from this send; the last told turn goes
	// with them, so the first turn of this task is told even when it ends
	// with the same message as the last turn of the one before.
	if entry, err = e.update(entry.ID, func(x *session.Entry) {
		x.TaskGeneration, x.AdmissionID = task.id(), task.id()
		x.Turns, x.IdleTurn = 0, ""
	}); err != nil {
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
			if err := e.opts.Deps.Clock.Sleep(ctx, e.opts.Deps.Pauses.Settle); err != nil {
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
		if err := e.opts.Deps.Clock.Sleep(ctx, e.opts.Deps.Pauses.Send); err != nil {
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
	if err := e.opts.Deps.Clock.Sleep(ctx, e.opts.Deps.Pauses.Key); err != nil {
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
		if err := e.opts.Deps.Clock.Sleep(ctx, e.opts.Deps.Pauses.Send); err != nil {
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
