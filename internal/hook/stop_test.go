package hook

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/session"
)

// stopSetup registers a lead and one of its peers with a task, and returns
// the Stop hook's environment in that peer's pane and the lead's address.
func stopSetup(t *testing.T, peer session.Entry) (Env, *session.Registry, *bus.Bus, string) {
	t.Helper()
	e, r, b := setup(t, "claude")
	SessionStart(context.Background(), e)
	l := lead(t, r, e)
	peer.ID, peer.Lead, peer.Agent, peer.Role = "peer-abc123", l.ID, "codex", "implement"
	if err := r.Record(peer); err != nil {
		t.Fatal(err)
	}
	// Mail sent to the lead before the stop is not the stop's.
	if _, err := b.Inbox(session.Address(l)); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"session_id": "peer-session-1", "hook_event_name": "Stop"})
	pane := Env{Root: e.Root, Agent: "codex", Input: input, Environ: []string{"DUOLOG_DEPTH=1", "DUOLOG_LEAD=" + l.ID, "DUOLOG_PEER=peer-abc123"}}
	return pane, r, b, session.Address(l)
}

func leadMail(t *testing.T, b *bus.Bus, address string) []bus.Message {
	t.Helper()
	lines, err := b.Inbox(address)
	if err != nil {
		t.Fatal(err)
	}
	var ms []bus.Message
	for _, line := range lines {
		var m bus.Message
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatal(err)
		}
		ms = append(ms, m)
	}
	return ms
}

// A peer whose turn ends while its task is unfinished, by the rule of peer
// status, tells its lead once.
func TestPeerStopWithUnfinishedTaskTellsTheLead(t *testing.T) {
	pane, _, b, address := stopSetup(t, session.Entry{Task: ".duolog/tasks/peer-abc123/task.md"})
	silent(t, Stop(context.Background(), pane))
	ms := leadMail(t, b, address)
	want := "peer-abc123 stopped; task .duolog/tasks/peer-abc123/task.md is unfinished"
	if len(ms) != 1 || ms[0].S != want || ms[0].K != "status" || ms[0].Ref != "peer-abc123" || ms[0].From != "codex/implement" {
		t.Fatalf("lead mail %+v, want one line %q", ms, want)
	}
}

// No line when the task is finished, the peer is closed, a wait already
// watches the peer, or the pane is no peer.
func TestPeerStopStaysQuiet(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry session.Entry
		watch bool
		env   []string
	}{
		{name: "finished", entry: session.Entry{}},
		{name: "closed", entry: session.Entry{Task: "t.md", Closed: true}},
		{name: "watched", entry: session.Entry{Task: "t.md"}, watch: true},
		{name: "no peer", entry: session.Entry{Task: "t.md"}, env: []string{"DUOLOG_DEPTH=1"}},
		{name: "lead", entry: session.Entry{Task: "t.md"}, env: []string{"DUOLOG_PEER=peer-abc123"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pane, r, b, address := stopSetup(t, tc.entry)
			if tc.env != nil {
				pane.Environ = tc.env
			}
			if tc.watch {
				release, err := r.Watch("peer-abc123")
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			}
			silent(t, Stop(context.Background(), pane))
			if ms := leadMail(t, b, address); len(ms) != 0 {
				t.Fatalf("lead mail %+v", ms)
			}
		})
	}
}
