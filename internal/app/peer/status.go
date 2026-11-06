package peer

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

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
