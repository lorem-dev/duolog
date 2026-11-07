package load

import (
	"testing"
)

func TestLineShowsAgentsWindowAndIdle(t *testing.T) {
	s := Summary{
		Agents: []Agent{
			{Name: "claude", Running: 3, Idle: 1, Limit: 12, Live: 7},
			{Name: "codex", Running: 0, Limit: 3, Live: 3},
		},
		Idle:   []string{"peer-ab12cd"},
		Window: 41, WindowKnown: true,
	}
	want := "duolog peers: claude 3 running (1 idle) of 12; codex 0 of 3; 5h 41%; idle: peer-ab12cd"
	if got := s.Line(); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestLineLeavesOutAgentsWithoutLimitAndUnknownWindow(t *testing.T) {
	s := Summary{Agents: []Agent{{Name: "claude", Running: 2, Limit: 0}, {Name: "codex", Running: 1, Limit: 3, Live: 3}}}
	want := "duolog peers: codex 1 running of 3"
	if got := s.Line(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := (Summary{Agents: []Agent{{Name: "claude", Limit: 12, Live: 12}}}).Line(); got != "duolog peers: claude 0 of 12" {
		t.Fatalf("idle agent with a limit: %q", got)
	}
	if got := (Summary{}).Line(); got != "" {
		t.Fatalf("nothing to say gave %q", got)
	}
}

func TestLineCountsPeersOfThisLeadAgainstAllLiveSessions(t *testing.T) {
	// Another lead holds 5 of the 12 sessions: free is 12 - 8 = 4, under half.
	s := Summary{Agents: []Agent{{Name: "claude", Running: 3, Limit: 12, Live: 8}}, Window: 10, WindowKnown: true}
	if got := s.Line(); got != "duolog peers: claude 3 running of 12; 5h 10%" {
		t.Fatalf("got %q", got)
	}
}

func TestFreeAdviceNeedsHalfFreeLowWindowAndEmptyQueue(t *testing.T) {
	base := Summary{Agents: []Agent{{Name: "claude", Running: 3, Limit: 12, Live: 3}}, Window: 41, WindowKnown: true}
	advice := " - 9 claude sessions free: start independent work or stack it on open pull request branches (duolog peer queue add)"
	if got, want := base.Line(), "duolog peers: claude 3 running of 12; 5h 41%"+advice; got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	queued := base
	queued.Queued = 1
	if got := queued.Line(); contains(got, "start independent work") {
		t.Fatalf("a ready queue still has work for the free sessions: %q", got)
	}
	hot := base
	hot.Window = 70
	if got := hot.Line(); contains(got, "free:") {
		t.Fatalf("a window at 70%% gets no advice: %q", got)
	}
	full := Summary{Agents: []Agent{{Name: "claude", Running: 6, Limit: 12, Live: 7}}}
	if got := full.Line(); contains(got, "free:") {
		t.Fatalf("5 free of 12 is under half: %q", got)
	}
	edge := Summary{Agents: []Agent{{Name: "claude", Running: 6, Limit: 12, Live: 6}}, Window: 69, WindowKnown: true}
	if got := edge.Line(); !contains(got, " - 6 claude sessions free:") {
		t.Fatalf("half free and 69%% is advised: %q", got)
	}
}

// A window not known may be at 92%: the line advises no more work then.
func TestNoFreeAdviceWithUnknownWindow(t *testing.T) {
	s := Summary{Agents: []Agent{{Name: "codex", Limit: 3, Live: 0}}}
	if got := s.Line(); got != "duolog peers: codex 0 of 3" {
		t.Fatalf("an unknown window got advice: %q", got)
	}
}

// Free counts an agent's free sessions, or the most free of any agent.
func TestFreeSessions(t *testing.T) {
	s := Summary{Agents: []Agent{{Name: "claude", Limit: 12, Live: 12}, {Name: "codex", Limit: 3, Live: 1}}}
	for agent, want := range map[string]int{"claude": 0, "codex": 2, "": 2, "gemini": 0} {
		if got := s.Free(agent); got != want {
			t.Errorf("Free(%q) = %d, want %d", agent, got, want)
		}
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

// Queued items show as "queue N" after the agents. While items wait, the line
// advises nothing, however many sessions are free: the hooks start the run
// themselves, and more work would only queue behind it.
func TestLineCountsTheQueueAndAdvisesNothing(t *testing.T) {
	s := Summary{
		Agents: []Agent{
			{Name: "claude", Running: 3, Limit: 12, Live: 3},
			{Name: "codex", Running: 0, Limit: 3, Live: 1},
		},
		Queued: 2, Window: 41, WindowKnown: true,
	}
	want := "duolog peers: claude 3 running of 12; codex 0 of 3; queue 2; 5h 41%"
	if got := s.Line(); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	unknown := s
	unknown.WindowKnown = false
	if got := unknown.Line(); contains(got, "sessions free") {
		t.Fatalf("an unknown window with a queue got advice: %q", got)
	}
	if got := (Summary{Agents: []Agent{{Name: "claude", Limit: 12}}}).Line(); contains(got, "; queue ") || contains(got, "queue run") {
		t.Fatalf("an empty queue is not named: %q", got)
	}
}
