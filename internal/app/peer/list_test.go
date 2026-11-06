package peer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/bus"
)

func TestListPeersReportsDetails(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.ready()
	b, err := bus.Open(filepath.Join(r.root, ".duolog"), r.cfg.Wire)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Send(bus.Message{From: "codex/lead.test", To: "codex/implement", K: "task", Ref: e.ID, S: "role implement task=t.md"}); err != nil {
		t.Fatal(err)
	}
	r.rex(withScreens(working()))
	peers, err := List(r.ctx)
	if err != nil || len(peers) != 1 {
		t.Fatalf("%+v %v", peers, err)
	}
	p := peers[0]
	if p.State != Starting || !p.Working || p.Display() != "working" {
		t.Fatalf("%+v %q", p, p.Display())
	}
	if p.Role != "implement" || p.Agent != "codex" || p.Tab != "duolog | codex-implement" {
		t.Fatalf("%+v", p)
	}
	if p.WorkRoot != r.entry(e.ID).WorkRoot {
		t.Fatalf("work root %q", p.WorkRoot)
	}
	if p.Last == nil || p.Last.Kind != "task" || p.Last.At.IsZero() {
		t.Fatalf("last %+v", p.Last)
	}
}

func TestListPeersQuestionWinsAndHasUnknownStart(t *testing.T) {
	r := newRig(t, baseScript())
	r.ready()
	r.rex(withScreens(queuedQuestion + working()))
	peers, err := List(r.ctx)
	if err != nil || len(peers) != 1 {
		t.Fatalf("%+v %v", peers, err)
	}
	p := peers[0]
	if p.State != Question || p.Display() != "asking" {
		t.Fatalf("%+v %q", p, p.Display())
	}
	if p.Last == nil || p.Last.Kind != "question" || !p.Last.At.IsZero() {
		t.Fatalf("last %+v", p.Last)
	}
}

// working is the display of a starting peer that works; any other state,
// updating included, shows itself.
func TestDisplayWorkingOnlyWhenStarting(t *testing.T) {
	tests := []struct {
		p    ListedPeer
		want string
	}{
		{ListedPeer{State: Starting, Working: true}, "working"},
		{ListedPeer{State: Starting}, "starting"},
		{ListedPeer{State: Updating, Working: true}, "updating"},
		{ListedPeer{State: Ready, Working: true}, "ready"},
		{ListedPeer{State: Question, Working: true}, "asking"},
	}
	for _, tt := range tests {
		if got := tt.p.Display(); got != tt.want {
			t.Errorf("%+v: %q, want %q", tt.p, got, tt.want)
		}
	}
}

// A question with no recorded start has no time in JSON, not year 1.
func TestLastEventJSONOmitsUnknownTime(t *testing.T) {
	data, err := json.Marshal(ListedPeer{ID: "peer-x", State: Question, Last: &LastEvent{Kind: "question"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"at"`) || !strings.Contains(string(data), `"last":{"kind":"question"}`) {
		t.Fatalf("%s", data)
	}
	at := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	data, err = json.Marshal(LastEvent{Kind: "task", At: at})
	if err != nil || string(data) != `{"kind":"task","at":"2026-11-06T10:00:00Z"}` {
		t.Fatalf("%s %v", data, err)
	}
}

func TestListPeersChangesNothing(t *testing.T) {
	r := newRig(t, baseScript())
	r.ready()
	sessions := filepath.Join(r.root, ".duolog", "sessions.json")
	busFile := filepath.Join(r.root, ".duolog", "bus.ndjson")
	beforeSessions, err := os.ReadFile(sessions)
	if err != nil {
		t.Fatal(err)
	}
	beforeBus, err := os.ReadFile(busFile)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	r.rex(withScreens(queuedQuestion + working()))
	if _, err := List(r.ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.keys()) != 0 || len(r.named("send")) != 0 {
		t.Fatalf("listing typed: %v", r.calls())
	}
	afterSessions, _ := os.ReadFile(sessions)
	afterBus, _ := os.ReadFile(busFile)
	if string(afterSessions) != string(beforeSessions) || string(afterBus) != string(beforeBus) {
		t.Fatal("listing changed the registry or the bus")
	}
}

// Integration review, Minor: list reads the registry, Rex and the bus's task
// times only. A missing bus stays missing, and a bus the caller may not
// write does not stop it; it still resolves the lead.
func TestListLeavesTheBusAlone(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.ready()
	dir := filepath.Join(r.root, ".duolog")
	busFile := filepath.Join(dir, "bus.ndjson")
	for _, name := range []string{"bus.ndjson", "cursors", "bus.lock"} {
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	r.rex(withScreens(codexIdle("")))
	if peers, err := List(r.ctx); err != nil || len(peers) != 1 || peers[0].ID != e.ID {
		t.Fatalf("%+v %v", peers, err)
	}
	for _, name := range []string{"bus.ndjson", "cursors", "bus.lock"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s was created: %v", name, err)
		}
	}

	// The task times still come from a bus the caller may only read.
	sent := time.Now().Add(time.Hour).UTC()
	line, _ := json.Marshal(bus.Message{K: "task", Ref: e.ID, T: sent.Format(time.RFC3339Nano)})
	if err := os.Remove(busFile); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.WriteFile(busFile, append(line, '\n'), 0400); err != nil {
		t.Fatal(err)
	}
	if f, err := os.OpenFile(busFile, os.O_RDWR, 0); err == nil {
		f.Close()
		t.Skip("this user can write a read-only file")
	}
	peers, err := List(r.ctx)
	if err != nil || len(peers) != 1 || peers[0].Last == nil || peers[0].Last.Kind != "task" || !peers[0].Last.At.Equal(sent) {
		t.Fatalf("list with a read-only bus: %+v %v", peers, err)
	}
}

func TestListTabMatchesOpenedLabel(t *testing.T) {
	r := newRig(t, baseScript())
	r.ready()
	spawns := r.spawns()
	if len(spawns) == 0 {
		t.Fatal("no tab was opened")
	}
	joined := strings.Join(spawns[0], "\x00")
	r.rex(withScreens(codexIdle("")))
	peers, err := List(r.ctx)
	if err != nil || len(peers) != 1 {
		t.Fatalf("%+v %v", peers, err)
	}
	if !strings.Contains(joined, peers[0].Tab) {
		t.Fatalf("tab %q not in %q", peers[0].Tab, spawns[0])
	}
}

func TestLastEventPicksNewest(t *testing.T) {
	old := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	newer := old.Add(time.Hour)
	tests := []struct {
		name  string
		entry session.Entry
		state State
		sent  time.Time
		want  *LastEvent
	}{
		{"nothing known", session.Entry{}, Ready, time.Time{}, nil},
		{"task is newer", session.Entry{UpdatedAt: old}, Ready, newer, &LastEvent{Kind: "task", At: newer}},
		{"record is newer", session.Entry{UpdatedAt: newer}, Ready, old, &LastEvent{Kind: "record", At: newer}},
		{"question without start", session.Entry{UpdatedAt: newer}, Question, newer, &LastEvent{Kind: "question"}},
		{"question with start", session.Entry{QuestionSince: old}, Question, time.Time{}, &LastEvent{Kind: "question", At: old}},
		{"record newer than a known question", session.Entry{QuestionSince: old, UpdatedAt: old.Add(time.Hour)}, Question, old.Add(30 * time.Minute), &LastEvent{Kind: "record", At: old.Add(time.Hour)}},
		{"task newer than a known question", session.Entry{QuestionSince: old, UpdatedAt: old}, Question, newer, &LastEvent{Kind: "task", At: newer}},
		{"known question is newest", session.Entry{QuestionSince: newer, UpdatedAt: old}, Question, old, &LastEvent{Kind: "question", At: newer}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lastEvent(tt.entry, tt.state, tt.sent)
			if (got == nil) != (tt.want == nil) || got != nil && (got.Kind != tt.want.Kind || !got.At.Equal(tt.want.At)) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
