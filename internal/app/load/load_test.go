package load

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLineShowsAgentsWindowAndIdle(t *testing.T) {
	s := Summary{
		Agents: []Agent{
			{Name: "claude", Running: 3, Idle: 1, Limit: 12, Live: 3},
			{Name: "codex", Running: 0, Limit: 3, Live: 0},
		},
		Idle:   []string{"peer-ab12cd"},
		Window: 41, WindowKnown: true,
	}
	want := "duolog peers: claude 3 running (1 idle) of 12; codex 0 of 3; 5h 41%; idle: peer-ab12cd"
	if got := s.Line(false); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestLineLeavesOutAgentsWithoutLimitAndUnknownWindow(t *testing.T) {
	s := Summary{Agents: []Agent{{Name: "claude", Running: 2, Limit: 0}, {Name: "codex", Running: 1, Limit: 3, Live: 1}}}
	want := "duolog peers: codex 1 running of 3"
	if got := s.Line(false); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := (Summary{Agents: []Agent{{Name: "claude", Limit: 12}}}).Line(false); got != "duolog peers: claude 0 of 12" {
		t.Fatalf("idle agent with a limit: %q", got)
	}
	if got := (Summary{}).Line(false); got != "" {
		t.Fatalf("nothing to say gave %q", got)
	}
}

func TestLineCountsPeersOfThisLeadAgainstAllLiveSessions(t *testing.T) {
	// Another lead holds 5 of the 12 sessions: free is 12 - 8 = 4, under half.
	s := Summary{Agents: []Agent{{Name: "claude", Running: 3, Limit: 12, Live: 8}}, Window: 10, WindowKnown: true}
	if got := s.Line(true); got != "duolog peers: claude 3 running of 12; 5h 10%" {
		t.Fatalf("got %q", got)
	}
}

func TestFreeAdviceNeedsHalfFreeLowWindowAndEmptyQueue(t *testing.T) {
	base := Summary{Agents: []Agent{{Name: "claude", Running: 3, Limit: 12, Live: 3}}, Window: 41, WindowKnown: true}
	advice := " - 9 claude sessions free: start independent work or stack it on open pull request branches (duolog peer queue add)"
	if got, want := base.Line(true), "duolog peers: claude 3 running of 12; 5h 41%"+advice; got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if got := base.Line(false); contains(got, "free:") {
		t.Fatalf("a ready queue still has work for the free sessions: %q", got)
	}
	hot := base
	hot.Window = 70
	if got := hot.Line(true); contains(got, "free:") {
		t.Fatalf("a window at 70%% gets no advice: %q", got)
	}
	full := Summary{Agents: []Agent{{Name: "claude", Running: 6, Limit: 12, Live: 7}}}
	if got := full.Line(true); contains(got, "free:") {
		t.Fatalf("5 free of 12 is under half: %q", got)
	}
	edge := Summary{Agents: []Agent{{Name: "claude", Running: 6, Limit: 12, Live: 6}}, Window: 69, WindowKnown: true}
	if got := edge.Line(true); !contains(got, " - 6 claude sessions free:") {
		t.Fatalf("half free and 69%% is advised: %q", got)
	}
}

func TestFreeAdviceWithUnknownWindow(t *testing.T) {
	s := Summary{Agents: []Agent{{Name: "codex", Limit: 3, Live: 0}}}
	if got := s.Line(true); !contains(got, " - 3 codex sessions free:") {
		t.Fatalf("an unknown window does not block the advice: %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestQueueEmpty(t *testing.T) {
	dir := t.TempDir()
	if !QueueEmpty(dir) {
		t.Fatal("a missing file is an empty queue")
	}
	path := filepath.Join(dir, "peer-queue.ndjson")
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("")
	if !QueueEmpty(dir) {
		t.Fatal("an empty file is an empty queue")
	}
	write(`{"id":"q1","state":"opened peer-ab"}` + "\n" + `{"id":"q2","state":"failed: no"}` + "\n" + "not json\n")
	if !QueueEmpty(dir) {
		t.Fatal("opened and failed items are not ready")
	}
	write(`{"id":"q1","state":"opened peer-ab"}` + "\n" + `{"id":"q2","state":"queued"}` + "\n")
	if QueueEmpty(dir) {
		t.Fatal("a queued item is ready")
	}
}

func TestDueSaysOnceAndAgainAfterFiveMinutesOrAChange(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	a, b := "duolog peers: claude 3 running of 12", "duolog peers: claude 4 running of 12"
	if !Due(dir, "lead1", a, now) {
		t.Fatal("the first line is due")
	}
	if Due(dir, "lead1", a, now.Add(time.Minute)) {
		t.Fatal("the same line one minute later is not due")
	}
	if !Due(dir, "lead1", b, now.Add(2*time.Minute)) {
		t.Fatal("a changed number is due at once")
	}
	if Due(dir, "lead1", b, now.Add(6*time.Minute-time.Second)) {
		t.Fatal("not due before 5 minutes")
	}
	if !Due(dir, "lead1", b, now.Add(7*time.Minute)) {
		t.Fatal("due 5 minutes after the last one")
	}
	if !Due(dir, "lead2", b, now.Add(7*time.Minute)) {
		t.Fatal("each lead has its own clock")
	}
}

func TestMarkRecordsALineSeenByPrompt(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	line := "duolog peers: claude 3 running of 12"
	Mark(dir, "lead1", line, now)
	if Due(dir, "lead1", line, now.Add(time.Minute)) {
		t.Fatal("a line the prompt hook just showed is not due again")
	}
}
