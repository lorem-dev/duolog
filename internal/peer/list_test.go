package peer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/session"
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
