package hook

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// allMail reads every message the lead has not read.
func allMail(t *testing.T, b *bus.Bus, address string) []bus.Message {
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

// leadMail is the lead's unread mail without the peer-idle and peer-exit
// events, which have tests of their own.
func leadMail(t *testing.T, b *bus.Bus, address string) []bus.Message {
	t.Helper()
	var ms []bus.Message
	for _, m := range allMail(t, b, address) {
		if m.K != "peer-idle" && m.K != "peer-exit" {
			ms = append(ms, m)
		}
	}
	return ms
}

// kindMail is the lead's unread mail of one kind.
func kindMail(t *testing.T, b *bus.Bus, address, kind string) []bus.Message {
	t.Helper()
	var ms []bus.Message
	for _, m := range allMail(t, b, address) {
		if m.K == kind {
			ms = append(ms, m)
		}
	}
	return ms
}

func stopInput(t *testing.T, transcript, last string) []byte {
	t.Helper()
	input, err := json.Marshal(map[string]any{"session_id": "peer-session-1", "hook_event_name": "Stop", "transcript_path": transcript, "last_assistant_message": last})
	if err != nil {
		t.Fatal(err)
	}
	return input
}

// A peer's turn end posts one peer-idle message with the checkout relative to
// the project and the first line of the last message, cut to 160 characters.
func TestPeerStopPostsPeerIdle(t *testing.T) {
	pane, r, b, address := stopSetup(t, session.Entry{})
	work := filepath.Join(pane.Root, ".worktrees", "feat-x")
	if _, err := r.Update("peer-abc123", func(e *session.Entry) { e.WorkRoot = work }); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(transcript, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pane.Input = stopInput(t, transcript, "\n  "+strings.Repeat("x", 200)+"\nsecond line")
	var called []string
	pane.PeerIdle = func(_ context.Context, lead, peer string) { called = append(called, lead+" "+peer) }
	silent(t, Stop(context.Background(), pane))
	ms := kindMail(t, b, address, "peer-idle")
	want := "peer-abc123 idle in .worktrees/feat-x: " + strings.Repeat("x", 160)
	if len(ms) != 1 || ms[0].S != want || ms[0].Ref != "peer-abc123" || ms[0].From != "codex/implement" {
		t.Fatalf("peer-idle mail %+v, want one line %q", ms, want)
	}
	if len(called) != 1 || !strings.HasSuffix(called[0], " peer-abc123") {
		t.Fatalf("PeerIdle calls %v, want one", called)
	}
}

// Without a last message the line is only the place. A Stop that runs again
// for the same transcript length posts nothing; a longer transcript posts.
func TestPeerStopPeerIdleOncePerTurn(t *testing.T) {
	pane, _, b, address := stopSetup(t, session.Entry{})
	transcript := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(transcript, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pane.Input = stopInput(t, transcript, "")
	var calls int
	pane.PeerIdle = func(context.Context, string, string) { calls++ }
	silent(t, Stop(context.Background(), pane))
	silent(t, Stop(context.Background(), pane))
	ms := kindMail(t, b, address, "peer-idle")
	if len(ms) != 1 || ms[0].S != "peer-abc123 idle in ." || calls != 1 {
		t.Fatalf("peer-idle mail %+v, %d calls, want one line for one turn", ms, calls)
	}
	if err := os.WriteFile(transcript, []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	silent(t, Stop(context.Background(), pane))
	if ms := kindMail(t, b, address, "peer-idle"); len(ms) != 1 || calls != 2 {
		t.Fatalf("peer-idle mail %+v, %d calls, want one line for the next turn", ms, calls)
	}
}

// A closed peer and a pane that is no peer post no peer-idle.
func TestPeerStopPeerIdleStaysQuiet(t *testing.T) {
	pane, _, b, address := stopSetup(t, session.Entry{Closed: true})
	silent(t, Stop(context.Background(), pane))
	pane.Environ = []string{"DUOLOG_DEPTH=1"}
	silent(t, Stop(context.Background(), pane))
	if ms := kindMail(t, b, address, "peer-idle"); len(ms) != 0 {
		t.Fatalf("peer-idle mail %+v", ms)
	}
}

// The lead's next prompt hook posts peer-exit once for a peer that exited or
// is gone, and not for a live one or a closed one.
func TestLeadHookPostsPeerExitOnce(t *testing.T) {
	e, r, _ := setup(t, "claude")
	SessionStart(context.Background(), e)
	l := lead(t, r, e)
	for _, id := range []string{"peer-exit01", "peer-live01", "peer-gone01", "peer-shut01"} {
		x := session.Entry{ID: id, Lead: l.ID, Agent: "claude", Role: "review", Closed: id == "peer-shut01"}
		if err := r.Record(x); err != nil {
			t.Fatal(err)
		}
	}
	e.Peers = func(_ context.Context, leadID string) []string {
		if leadID != l.ID {
			t.Errorf("Peers asked for lead %q, want %q", leadID, l.ID)
		}
		return []string{"peer-exit01 exited", "peer-live01 ready", "peer-gone01 gone", "peer-shut01 exited"}
	}
	first := content(t, Prompt(context.Background(), e), "UserPromptSubmit")
	for _, want := range []string{"peer-exit peer-exit01: peer-exit01 exited in .", "peer-exit peer-gone01: peer-gone01 exited in ."} {
		if !strings.Contains(first, want) {
			t.Fatalf("first prompt hook output %q lacks %q", first, want)
		}
	}
	if strings.Contains(first, "peer-live01") || strings.Contains(first, "peer-shut01") {
		t.Fatalf("first prompt hook output %q names a live or closed peer", first)
	}
	silent(t, Prompt(context.Background(), e))
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
