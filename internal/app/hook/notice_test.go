package hook

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/runsnap"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/store"
)

func run(id, role, state string) runsnap.Run { return runsnap.Run{ID: id, Role: role, State: state} }

// fastPeer is a running peer as the fast snapshot shows it.
func fastPeer(id string) runsnap.Peer {
	return runsnap.Peer{ID: id, State: "unknown"}
}

// mk is a mark with no nil lists, as changes writes it.
func mk(ended, running, idle []string, asks int) noticeMark {
	orEmpty := func(l []string) []string {
		if l == nil {
			return []string{}
		}
		return l
	}
	return noticeMark{Ended: orEmpty(ended), Running: orEmpty(running), Idle: orEmpty(idle), Asks: asks}
}

func TestChanges(t *testing.T) {
	five := runsnap.Snapshot{}
	for _, id := range []string{"r1", "r2", "r3", "r4", "r5"} {
		five.Runs = append(five.Runs, run(id, "review", "done"))
	}
	long := strings.Repeat("x", 120)
	for _, tc := range []struct {
		name  string
		snap  runsnap.Snapshot
		mark  *noticeMark
		turns turns
		text  string
		next  noticeMark
	}{
		{"first look is quiet", runsnap.Snapshot{Runs: []runsnap.Run{run("r1", "review", "done"), run("r2", "review", "running")}, Peers: []runsnap.Peer{fastPeer("p1")}},
			nil, turns{"p1": "t1"}, "", mk([]string{"r1"}, []string{"r2"}, []string{"p1 t1"}, 0)},
		{"first look names asks", runsnap.Snapshot{AsksOpen: 2}, nil, nil, "duolog: 2 asks wait for an answer", mk(nil, nil, nil, 2)},
		{"one ask", runsnap.Snapshot{AsksOpen: 1}, nil, nil, "duolog: 1 ask waits for an answer", mk(nil, nil, nil, 1)},
		{"new failed run", runsnap.Snapshot{Runs: []runsnap.Run{run("r1", "review", "done"), run("r2", "review", "failed")}},
			&noticeMark{Ended: []string{"r1"}}, nil, "duolog: run r2 (review) failed", mk([]string{"r1", "r2"}, nil, nil, 0)},
		{"run needs input", runsnap.Snapshot{Runs: []runsnap.Run{run("r2", "", "needs")}}, &noticeMark{}, nil, "duolog: run r2 needs input", mk([]string{"r2"}, nil, nil, 0)},
		// A run here started no worker; the lead already has its answer.
		{"run here is quiet, lost is told", runsnap.Snapshot{Runs: []runsnap.Run{run("a", "x", "run_here"), run("b", "x", "lost")}}, &noticeMark{}, nil,
			"duolog: run b (x) lost", mk([]string{"a", "b"}, nil, nil, 0)},
		{"run here alone is quiet", runsnap.Snapshot{Runs: []runsnap.Run{run("a", "plan", "run_here")}}, &noticeMark{}, nil,
			"", mk([]string{"a"}, nil, nil, 0)},
		// The fast snapshot reports every running pane as unknown and not
		// working: that is not idle. Only a recorded end of turn is.
		{"fast peer is not idle", runsnap.Snapshot{Peers: []runsnap.Peer{fastPeer("p1")}}, &noticeMark{}, nil, "", mk(nil, nil, nil, 0)},
		{"recorded idle peer", runsnap.Snapshot{Peers: []runsnap.Peer{fastPeer("p1")}}, &noticeMark{}, turns{"p1": "t1"}, "duolog: peer p1 is idle", mk(nil, nil, []string{"p1 t1"}, 0)},
		{"idle peer seen", runsnap.Snapshot{Peers: []runsnap.Peer{fastPeer("p1")}}, &noticeMark{Idle: []string{"p1 t1"}}, turns{"p1": "t1"}, "", mk(nil, nil, []string{"p1 t1"}, 0)},
		{"next turn ends", runsnap.Snapshot{Peers: []runsnap.Peer{fastPeer("p1")}}, &noticeMark{Idle: []string{"p1 t1"}}, turns{"p1": "t2"}, "duolog: peer p1 is idle", mk(nil, nil, []string{"p1 t2"}, 0)},
		{"working peer is not idle", runsnap.Snapshot{Peers: []runsnap.Peer{{ID: "p1", State: "working", Working: true}}}, &noticeMark{Idle: []string{"p1 t1"}}, turns{"p1": "t2"}, "", mk(nil, nil, []string{"p1 t1"}, 0)},
		{"exited peer is not idle", runsnap.Snapshot{Peers: []runsnap.Peer{{ID: "p1", State: "exited"}}}, &noticeMark{}, turns{"p1": "t1"}, "", mk(nil, nil, nil, 0)},
		{"same ask", runsnap.Snapshot{AsksOpen: 1}, &noticeMark{Asks: 1}, nil, "", mk(nil, nil, nil, 1)},
		{"ask answered", runsnap.Snapshot{}, &noticeMark{Asks: 1}, nil, "", mk(nil, nil, nil, 0)},
		{"unknown ids drop out", runsnap.Snapshot{}, &noticeMark{Ended: []string{"gone"}, Idle: []string{"gone t"}}, nil, "", mk(nil, nil, nil, 0)},
		{"five runs", five, &noticeMark{}, nil, "duolog: run r1 (review) done, run r2 (review) done, run r3 (review) done and 2 more",
			mk([]string{"r1", "r2", "r3", "r4", "r5"}, nil, nil, 0)},
		// A run that ends after five others fell out of the snapshot's newest
		// finished runs is still told, from the running ids the mark holds.
		{"run left the snapshot", runsnap.Snapshot{}, &noticeMark{Running: []string{"a"}}, nil, "duolog: run a ended", mk(nil, nil, nil, 0)},
		{"run left among others", runsnap.Snapshot{Runs: []runsnap.Run{run("b", "x", "running")}}, &noticeMark{Running: []string{"a", "b"}}, nil, "duolog: run a ended", mk(nil, []string{"b"}, nil, 0)},
		{"burst of seven", runsnap.Snapshot{}, &noticeMark{Running: []string{"a", "b", "c", "d", "e", "f", "g"}}, nil,
			"duolog: run a ended, run b ended, run c ended and 4 more", mk(nil, nil, nil, 0)},
		// A hook that read an older snapshot must not forget what a newer one told.
		{"stale snapshot keeps ended", runsnap.Snapshot{Runs: []runsnap.Run{run("r1", "x", "running")}}, &noticeMark{Ended: []string{"r1"}}, nil, "", mk([]string{"r1"}, []string{"r1"}, nil, 0)},
		{"stale snapshot keeps idle", runsnap.Snapshot{Peers: []runsnap.Peer{{ID: "p1", State: "working", Working: true}}}, &noticeMark{Idle: []string{"p1 t1"}}, nil, "", mk(nil, nil, []string{"p1 t1"}, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, next := changes(tc.snap, tc.mark, tc.turns)
			if text != tc.text {
				t.Errorf("text %q, want %q", text, tc.text)
			}
			if !reflect.DeepEqual(next, tc.next) {
				t.Errorf("next %+v, want %+v", next, tc.next)
			}
		})
	}
	t.Run("text stays short", func(t *testing.T) {
		s := runsnap.Snapshot{AsksOpen: 3}
		for _, id := range []string{long + "1", long + "2", long + "3", long + "4"} {
			s.Runs = append(s.Runs, run(id, long, "failed"))
		}
		text, _ := changes(s, &noticeMark{}, nil)
		if text == "" || len(text) > 200 || !strings.HasPrefix(text, "duolog: ") {
			t.Fatalf("%d bytes: %q", len(text), text)
		}
	})
}

// A hook is a lead's, registered, with a snapshot that says what changed.
func noticeEnv(t *testing.T, agent string, snap func(context.Context, string) (runsnap.Snapshot, error)) Env {
	t.Helper()
	e, _, _ := setup(t, agent)
	SessionStart(context.Background(), e)
	e.Snapshot = snap
	return e
}

func ledSnapshot(s runsnap.Snapshot) func(context.Context, string) (runsnap.Snapshot, error) {
	return func(_ context.Context, leadID string) (runsnap.Snapshot, error) {
		s.Lead = &runsnap.Lead{ID: leadID}
		return s, nil
	}
}

func messageOf(t *testing.T, o Output) (message string, other map[string]json.RawMessage) {
	t.Helper()
	if len(o.Stdout) == 0 {
		return "", nil
	}
	if err := json.Unmarshal(o.Stdout, &other); err != nil {
		t.Fatalf("%q: %v", o.Stdout, err)
	}
	if raw, ok := other["systemMessage"]; ok {
		_ = json.Unmarshal(raw, &message)
	}
	return message, other
}

func TestPromptShowsNoticeOnce(t *testing.T) {
	e := noticeEnv(t, "claude", nil)
	e.Snapshot = ledSnapshot(runsnap.Snapshot{AsksOpen: 1})
	e.Load = nil
	first := Prompt(context.Background(), e)
	msg, other := messageOf(t, first)
	if msg != "duolog: 1 ask waits for an answer" {
		t.Fatalf("first prompt: %q", first.Stdout)
	}
	_ = other
	if second := Prompt(context.Background(), e); strings.Contains(string(second.Stdout), "systemMessage") {
		t.Fatalf("second prompt repeats it: %q", second.Stdout)
	}
	home := project.LeadHome(e.Root)
	if _, err := os.Stat(filepath.Join(home, "notices", shownName(id(e))+".json")); err != nil {
		t.Fatal(err)
	}
}

func TestPromptNoticeBesideContext(t *testing.T) {
	e, r, b := setup(t, "claude")
	SessionStart(context.Background(), e)
	e.Snapshot = ledSnapshot(runsnap.Snapshot{AsksOpen: 1})
	send(t, b, session.Address(lead(t, r, e)), "hello from a peer")
	out := Prompt(context.Background(), e)
	msg, other := messageOf(t, out)
	if msg == "" {
		t.Fatalf("no notice: %q", out.Stdout)
	}
	if _, ok := other["hookSpecificOutput"]; !ok {
		t.Fatalf("the context is gone: %q", out.Stdout)
	}
}

func TestLeadStopNoticeCodex(t *testing.T) {
	e := noticeEnv(t, "codex", nil)
	n := 0
	e.Snapshot = func(_ context.Context, leadID string) (runsnap.Snapshot, error) {
		n++
		s := runsnap.Snapshot{Lead: &runsnap.Lead{ID: leadID}, Runs: []runsnap.Run{run("r1", "review", "done")}}
		if n > 1 {
			s.Runs = append(s.Runs, run("r2", "review", "done"))
		}
		return s, nil
	}
	if out := Stop(context.Background(), e); len(out.Stdout) != 0 {
		t.Fatalf("first look: %q", out.Stdout)
	}
	out := Stop(context.Background(), e)
	if got := string(out.Stdout); got != "{\"systemMessage\":\"duolog: run r2 (review) done\"}\n" {
		t.Fatalf("stdout %q", got)
	}
	if out.Code != 0 || len(out.Stderr) != 0 {
		t.Fatalf("%+v", out)
	}
}

func TestClaudeStopNoticeNoContext(t *testing.T) {
	e := noticeEnv(t, "claude", nil)
	e.Snapshot = ledSnapshot(runsnap.Snapshot{AsksOpen: 2})
	msg, other := messageOf(t, Stop(context.Background(), e))
	if msg != "duolog: 2 asks wait for an answer" {
		t.Fatalf("message %q", msg)
	}
	for _, key := range []string{"hookSpecificOutput", "decision"} {
		if _, ok := other[key]; ok {
			t.Fatalf("a Stop notice must not carry %s", key)
		}
	}
}

// Only the lead's Stop asks for an alert: the person may have left while the
// lead worked, and a prompt or a session start finds them at the terminal.
// lead.notify false keeps the line and drops the alert.
func TestStopAlertsOnlyOnStop(t *testing.T) {
	sequence := func(o Output) (string, bool) {
		t.Helper()
		_, other := messageOf(t, o)
		raw, ok := other["terminalSequence"]
		var s string
		_ = json.Unmarshal(raw, &s)
		return s, ok
	}
	e := noticeEnv(t, "claude", nil)
	e.Snapshot = ledSnapshot(runsnap.Snapshot{AsksOpen: 1})
	if out := Prompt(context.Background(), e); len(out.Stdout) == 0 {
		t.Fatal("no notice on the prompt")
	} else if s, ok := sequence(out); ok {
		t.Fatalf("a prompt alerted: %q", s)
	}
	e.Snapshot = ledSnapshot(runsnap.Snapshot{AsksOpen: 2})
	if s, ok := sequence(Stop(context.Background(), e)); !ok || s != "\x1b]9;duolog: 2 asks wait for an answer\a" {
		t.Fatalf("lead Stop: %q, %v", s, ok)
	}
	off := false
	e.Config = func(root string) (*config.Config, error) {
		cfg, err := configfile.Load(root)
		if err == nil {
			cfg.Lead.Notify = &off
		}
		return cfg, err
	}
	e.Snapshot = ledSnapshot(runsnap.Snapshot{AsksOpen: 3})
	out := Stop(context.Background(), e)
	if msg, _ := messageOf(t, out); msg != "duolog: 3 asks wait for an answer" {
		t.Fatalf("lead.notify false dropped the line: %q", out.Stdout)
	}
	if s, ok := sequence(out); ok {
		t.Fatalf("lead.notify false alerted: %q", s)
	}
}

// The config that decides the alert is read within the notice's own 2 second
// budget: a config load that hangs leaves the line and drops the alert, and
// the hook does not wait for it.
func TestStopAlertConfigWithinBudget(t *testing.T) {
	e := noticeEnv(t, "claude", nil)
	e.Snapshot = ledSnapshot(runsnap.Snapshot{AsksOpen: 1})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var mu sync.Mutex
	calls := 0
	e.Config = func(root string) (*config.Config, error) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		// The first load is leadStop's; the one that decides the alert hangs.
		if n > 1 {
			<-release
		}
		return configfile.Load(root)
	}
	start := time.Now()
	out := Stop(context.Background(), e)
	if took := time.Since(start); took > snapshotWait+3*time.Second {
		t.Fatalf("Stop waited %s for the config", took)
	}
	msg, other := messageOf(t, out)
	if msg != "duolog: 1 ask waits for an answer" {
		t.Fatalf("line: %q", out.Stdout)
	}
	if _, ok := other["terminalSequence"]; ok {
		t.Fatalf("alerted without a config: %q", out.Stdout)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("%d config loads, want leadStop's and the alert's", calls)
	}
}

// TestNoticeSkips checks the sessions and snapshots that give no notice.
func TestNoticeSkips(t *testing.T) {
	snap := func(e Env) func(context.Context, string) (runsnap.Snapshot, error) {
		return ledSnapshot(runsnap.Snapshot{AsksOpen: 1})
	}
	for _, name := range []string{"worker", "peer", "unregistered", "nil snapshot", "snapshot error", "snapshot panic", "slow snapshot", "no lead", "subagent"} {
		t.Run(name, func(t *testing.T) {
			e := noticeEnv(t, "claude", nil)
			e.Snapshot = snap(e)
			switch name {
			case "worker":
				e.Environ = append(e.Environ, "DUOLOG_DEPTH=1")
			case "peer":
				e.Environ = append(e.Environ, "DUOLOG_PEER=p1")
			case "unregistered":
				// Not interactive, so its prompt registers no lead either.
				e = change(t, e, "session_id", "stranger")
				e.Environ = []string{"CLAUDE_CODE_ENTRYPOINT=sdk-cli"}
			case "nil snapshot":
				e.Snapshot = nil
			case "snapshot error":
				e.Snapshot = func(context.Context, string) (runsnap.Snapshot, error) {
					return runsnap.Snapshot{Lead: &runsnap.Lead{ID: id(e)}, AsksOpen: 1}, context.DeadlineExceeded
				}
			case "snapshot panic":
				e.Snapshot = func(context.Context, string) (runsnap.Snapshot, error) { panic("boom") }
			case "slow snapshot":
				if testing.Short() {
					t.Skip("waits for the 2 second limit")
				}
				e.Snapshot = func(ctx context.Context, _ string) (runsnap.Snapshot, error) {
					time.Sleep(2500 * time.Millisecond)
					return runsnap.Snapshot{Lead: &runsnap.Lead{ID: id(e)}, AsksOpen: 1}, nil
				}
			case "no lead":
				e.Snapshot = func(context.Context, string) (runsnap.Snapshot, error) { return runsnap.Snapshot{AsksOpen: 1}, nil }
			case "subagent":
				e = change(t, e, "agent_id", "sub-1")
			}
			if text := notice(context.Background(), e, id(e)); text != "" {
				t.Fatalf("notice %q", text)
			}
			if out := Prompt(context.Background(), e); strings.Contains(string(out.Stdout), "systemMessage") {
				t.Fatalf("prompt shows it: %q", out.Stdout)
			}
		})
	}
}

// The hook's snapshot is the fast one: its peer is unknown and not working.
// The person hears of an idle peer when the peer's Stop hook records its turn.
func TestNoticeIdleFollowsTheRecordedTurn(t *testing.T) {
	e, reg, _ := setup(t, "claude")
	SessionStart(context.Background(), e)
	if err := reg.Record(session.Entry{ID: "p1", Lead: id(e), Agent: "claude", Role: "review", Block: "block:p1"}); err != nil {
		t.Fatal(err)
	}
	e.Snapshot = ledSnapshot(runsnap.Snapshot{Peers: []runsnap.Peer{fastPeer("p1")}})
	ask := func() string { return notice(context.Background(), e, id(e)) }
	if text := ask(); text != "" {
		t.Fatalf("first look: %q", text)
	}
	if text := ask(); text != "" {
		t.Fatalf("a fast peer that works is not idle: %q", text)
	}
	if _, _, err := reg.ClaimIdle("p1", "turn-1"); err != nil {
		t.Fatal(err)
	}
	if text := ask(); text != "duolog: peer p1 is idle" {
		t.Fatalf("after the recorded turn: %q", text)
	}
	if text := ask(); text != "" {
		t.Fatalf("told twice: %q", text)
	}
	if _, _, err := reg.ClaimIdle("p1", "turn-2"); err != nil {
		t.Fatal(err)
	}
	if text := ask(); text != "duolog: peer p1 is idle" {
		t.Fatalf("after the next turn: %q", text)
	}
}

func TestPruneRemovesOldNoticeRecords(t *testing.T) {
	e, _, _ := setup(t, "claude")
	SessionStart(context.Background(), e)
	dir := filepath.Join(project.LeadHome(e.Root), noticeDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-store.KeepFor - time.Hour)
	for _, name := range []string{"dead.json", "dead.lock", "live.json"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if name != "live.json" {
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	prune(e)
	for name, want := range map[string]bool{"dead.json": false, "dead.lock": false, "live.json": true} {
		if _, err := os.Stat(filepath.Join(dir, name)); (err == nil) != want {
			t.Errorf("%s exists = %v, want %v", name, err == nil, want)
		}
	}
}
