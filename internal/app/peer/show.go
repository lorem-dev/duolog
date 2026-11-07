package peer

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	corepane "github.com/lorem-dev/duolog/internal/core/pane"
)

const (
	// DefaultShowLines is how many lines Show returns when the caller names
	// none; MaxShowLines is the most it will return.
	DefaultShowLines = 40
	MaxShowLines     = 200
	maxShowRunes     = 300
	maxShowBytes     = 64 * 1024
	cutMark          = " [cut]"
)

// Shown is a bounded, cleaned reading of one peer's pane.
type Shown struct {
	ID, Agent, Role string
	State           State    // from Parse, or Exited
	Lines           []string // oldest first, already cut
	Cut             bool     // true when lines were dropped for maxShowBytes
}

// Text is what `peer show` prints: the header and the lines, each ending in
// a newline. It is at most maxShowBytes long.
func (s Shown) Text() string {
	var b strings.Builder
	b.WriteString(s.Header() + "\n")
	for _, l := range s.Lines {
		b.WriteString(l + "\n")
	}
	return b.String()
}

// Show reads the last lines of a peer's pane for the person's terminal. It
// resolves the peer without a lead, writes no record and uses only the exit
// probe and a capture: no key, no answer, no observe.
func Show(ctx context.Context, id string, lines int) (Shown, error) {
	if lines < 1 || lines > MaxShowLines {
		return Shown{}, fmt.Errorf("--lines must be 1 to %d", MaxShowLines)
	}
	e, err := setupViewer(ctx)
	if err != nil {
		return Shown{}, err
	}
	entry, err := e.find(id)
	if err != nil {
		return Shown{}, err
	}
	if entry.Closed {
		return Shown{}, fmt.Errorf("peer %s is closed", entry.ID)
	}
	pane := e.paneOf(entry)
	presence, err := e.panes.Probe(ctx, pane)
	if err != nil {
		return Shown{}, err
	}
	if presence == corepane.Gone {
		return Shown{}, fmt.Errorf("peer %s: its pane is gone", entry.ID)
	}
	shown := Shown{ID: entry.ID, Agent: entry.Agent, Role: entry.Role, State: Exited}
	if presence != corepane.Exited {
		// The state is read from the whole styled screen, as List does, so
		// that a short --lines cannot change it.
		screen, err := e.screen(ctx, entry)
		if e.panes.Gone(err) {
			return Shown{}, fmt.Errorf("peer %s: its pane is gone", entry.ID)
		}
		if err != nil {
			return Shown{}, err
		}
		shown.State = screen.State
	}
	raw, err := e.panes.Capture(ctx, pane, lines)
	if e.panes.Gone(err) {
		return Shown{}, fmt.Errorf("peer %s: its pane is gone", entry.ID)
	}
	if err != nil {
		return Shown{}, err
	}
	shown.Lines = cutLines(strings.Split(string(raw), "\n"))
	shown.fit()
	return shown, nil
}

// cutLines drops blank lines and cuts each other line so that, with its
// cutMark, it is at most maxShowRunes long.
func cutLines(in []string) []string {
	var out []string
	keep := maxShowRunes - utf8.RuneCountInString(cutMark)
	for _, l := range in {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if utf8.RuneCountInString(l) > maxShowRunes {
			l = string([]rune(l)[:keep]) + cutMark
		}
		out = append(out, l)
	}
	return out
}

// fit drops the oldest lines until everything printed fits in maxShowBytes:
// the header, the lines and a newline after each.
func (s *Shown) fit() {
	body := 0
	for _, l := range s.Lines {
		body += len(l) + 1
	}
	for len(s.Lines) > 0 && body+len(s.Header())+1 > maxShowBytes {
		body -= len(s.Lines[0]) + 1
		s.Lines = s.Lines[1:]
		s.Cut = true
	}
}

// Header is the one line printed above the lines.
func (s Shown) Header() string {
	h := fmt.Sprintf("%s %s-%s %s, last %d lines", s.ID, s.Agent, s.Role, s.State, len(s.Lines))
	if s.Cut {
		h += ", cut to 64 KiB"
	}
	return h
}
