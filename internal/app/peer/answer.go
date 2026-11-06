package peer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/lorem-dev/duolog/internal/session"
)

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
		if err := e.opts.Deps.Clock.Sleep(ctx, e.opts.Deps.Pauses.Settle); err != nil {
			return err
		}
		var err error
		if state, screen, err = e.observe(ctx, entry); err != nil {
			return err
		}
	}
}
