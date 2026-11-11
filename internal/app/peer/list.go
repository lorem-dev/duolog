package peer

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/bus"
	corepane "github.com/lorem-dev/duolog/internal/core/pane"
)

// LastEvent is the newest thing duolog knows about a peer.
type LastEvent struct {
	Kind string    `json:"kind"`        // "task", "question" or "record"
	At   time.Time `json:"at,omitzero"` // zero: a question with no recorded start, left out of JSON
}

// ListedPeer is a snapshot of a lead's peer in this project's registry.
// ID and State keep the values that scripts read; the rest is added data.
type ListedPeer struct {
	ID       string     `json:"id"`
	State    State      `json:"state"`
	Working  bool       `json:"working"`
	Role     string     `json:"role"`
	Agent    string     `json:"agent"`
	Task     string     `json:"task,omitempty"`
	Tab      string     `json:"tab"`
	WorkRoot string     `json:"work_root"`
	Last     *LastEvent `json:"last,omitempty"`
}

// Display is the state a person reads: asking for a question, working for a
// starting peer that works, else the state itself, updating included.
func (p ListedPeer) Display() string {
	switch {
	case p.State == Question:
		return "asking"
	case p.State == Starting && p.Working:
		return "working"
	}
	return string(p.State)
}

// tabLabel is the name of a peer's Rex tab. The entry stores no label.
func tabLabel(agent, role string) string {
	return "duolog | " + agent + "-" + role
}

// taskTimes reads the bus once and gives the newest task time per peer ref.
// The bus can lose old lines, so a peer may have no time.
func taskTimes(dir string) map[string]time.Time {
	times := map[string]time.Time{}
	f, err := os.Open(filepath.Join(dir, "bus.ndjson"))
	if err != nil {
		return times
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scan.Scan() {
		var m bus.Message
		if json.Unmarshal(scan.Bytes(), &m) != nil || m.K != "task" || m.Ref == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, m.T); err == nil {
			times[m.Ref] = t
		}
	}
	return times
}

// lastEvent picks the newest known event: the question's start, the task
// send time or the record's update time. A question whose start is unknown
// is shown as itself with no time, since a newer event would hide that the
// peer asks. A question wins a tie, then the task; nil when nothing is known.
func lastEvent(entry session.Entry, state State, sent time.Time) *LastEvent {
	var last *LastEvent
	if state == Question {
		if entry.QuestionSince.IsZero() {
			return &LastEvent{Kind: "question"}
		}
		last = &LastEvent{Kind: "question", At: entry.QuestionSince}
	}
	for _, e := range []LastEvent{{Kind: "task", At: sent}, {Kind: "record", At: entry.UpdatedAt}} {
		if !e.At.IsZero() && (last == nil || e.At.After(last.At)) {
			last = &e
		}
	}
	return last
}

// List observes states without sending keys, answering questions or changing
// session records. Rebuild uses this to leave busy and asking peers alone.
// It resolves the lead but opens no bus: it creates no bus state, and reads
// the task times from a bus the caller may only read. e.bus is nil.
func List(ctx context.Context) ([]ListedPeer, error) { return list(ctx, true) }

// ListFast is List with no screen capture: it only probes each pane, so a
// running pane is Unknown with Working false. A status line that polls uses it.
func ListFast(ctx context.Context) ([]ListedPeer, error) { return list(ctx, false) }

// listedState is the state of an entry's pane. A running pane is read from
// its screen when capture is true, else it is Unknown. working is true for a
// peer that shows it is at work; gone panes read as Gone.
func (e *env) listedState(ctx context.Context, entry session.Entry, capture bool) (State, bool, error) {
	presence, err := e.panes.Probe(ctx, e.paneOf(entry))
	if err != nil {
		return Gone, false, err
	}
	switch presence {
	case corepane.Exited:
		return Exited, false, nil
	case corepane.Running:
		if !capture {
			return Unknown, false, nil
		}
		screen, err := e.screen(ctx, entry)
		if e.panes.Gone(err) {
			return Gone, false, nil // the pane went between the probe and the capture
		}
		if err != nil {
			return Gone, false, err
		}
		return screen.State, screen.Working, nil
	}
	return Gone, false, nil
}

func list(ctx context.Context, capture bool) ([]ListedPeer, error) {
	e, err := setupRecords(ctx, true)
	if err != nil {
		return nil, err
	}
	entries, err := e.reg.Entries()
	if err != nil {
		return nil, err
	}
	sent := taskTimes(e.dir)
	peers := []ListedPeer{}
	for _, entry := range entries {
		// A closed entry stays listed: its pane may be gone, as when the Rex
		// server restarted and the lead's process ended with it, and its
		// record and account slot stay until peer close.
		if entry.Block == "" || entry.Lead != e.leadID {
			continue
		}
		state, working, err := e.listedState(ctx, entry, capture)
		if err != nil {
			return nil, err
		}
		peers = append(peers, ListedPeer{
			ID: entry.ID, State: state, Working: working,
			Role: entry.Role, Agent: entry.Agent, Task: entry.Task,
			Tab: tabLabel(entry.Agent, entry.Role), WorkRoot: entry.WorkRoot,
			Last: lastEvent(entry, state, sent[entry.ID]),
		})
	}
	return peers, nil
}
