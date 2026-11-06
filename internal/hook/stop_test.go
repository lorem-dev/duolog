package hook

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
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

// A peer that ends three turns on one task tells the lead once. A new task
// generation, sent later, is told again.
func TestPeerStopTellsOncePerTask(t *testing.T) {
	pane, r, b, address := stopSetup(t, session.Entry{Task: "tasks/t.md", TaskGeneration: "gen-1"})
	for range 3 {
		silent(t, Stop(context.Background(), pane))
	}
	if ms := leadMail(t, b, address); len(ms) != 1 {
		t.Fatalf("lead mail %+v, want one line for three turn ends", ms)
	}
	if _, err := r.Update("peer-abc123", func(e *session.Entry) { e.Task, e.TaskGeneration = "tasks/t.md", "gen-2" }); err != nil {
		t.Fatal(err)
	}
	silent(t, Stop(context.Background(), pane))
	silent(t, Stop(context.Background(), pane))
	if ms := leadMail(t, b, address); len(ms) != 1 || ms[0].S != "peer-abc123 stopped; task tasks/t.md is unfinished" {
		t.Fatalf("lead mail %+v, want one line for the new task", ms)
	}
}

// holdLock takes the named lock of the state folder, as another process that
// stalls with it would, until release is called or the test ends.
func holdLock(t *testing.T, dir, name string) (release func()) {
	t.Helper()
	l := flock.New(filepath.Join(dir, name))
	if err := l.Lock(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return func() { _ = l.Close() }
}

// stopReturns runs Stop and fails the test when it does not return soon.
func stopReturns(t *testing.T, ctx context.Context, pane Env) {
	t.Helper()
	done := make(chan Output, 1)
	go func() { done <- Stop(ctx, pane) }()
	select {
	case o := <-done:
		silent(t, o)
	case <-time.After(5 * time.Second):
		t.Fatal("Stop waited for a held lock")
	}
}

// A bus or registry lock that another process holds never holds back the
// peer's stop: the line is dropped. So does a cancellation after Stop began
// to wait. A dropped line does not use up the task's one line: the next stop,
// once the lock is free, sends it, and only once.
func TestPeerStopReturnsWithAHeldLock(t *testing.T) {
	for _, name := range []string{"bus.lock", "sessions.lock"} {
		t.Run(name, func(t *testing.T) {
			pane, _, b, address := stopSetup(t, session.Entry{Task: "t.md"})
			release := holdLock(t, filepath.Join(pane.Root, ".duolog"), name)
			stopReturns(t, context.Background(), pane)
			release()
			stopReturns(t, context.Background(), pane)
			stopReturns(t, context.Background(), pane)
			if ms := leadMail(t, b, address); len(ms) != 1 || ms[0].S != "peer-abc123 stopped; task t.md is unfinished" {
				t.Fatalf("lead mail %+v, want one line once the lock is free", ms)
			}
		})
		t.Run(name+" cancelled", func(t *testing.T) {
			pane, _, _, _ := stopSetup(t, session.Entry{Task: "t.md"})
			saved := store.NoticeWait
			store.NoticeWait = time.Minute
			t.Cleanup(func() { store.NoticeWait = saved })
			holdLock(t, filepath.Join(pane.Root, ".duolog"), name)
			ctx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(100*time.Millisecond, cancel)
			stopReturns(t, ctx, pane)
		})
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
