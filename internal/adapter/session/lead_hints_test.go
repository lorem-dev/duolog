package session

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
)

// withSessionEnv sets SessionEnvNames for one test.
func withSessionEnv(t *testing.T, names ...string) {
	t.Helper()
	old := SessionEnvNames
	SessionEnvNames = names
	t.Cleanup(func() { SessionEnvNames = old })
}

// TestResolveLeadHintsSharedProcess checks two Codex leads that share the
// app-server's process: the walk alone cannot tell them apart, a hint or an
// environment session ID can, and a hint that names no lead is no error.
func TestResolveLeadHintsSharedProcess(t *testing.T) {
	withSessionEnv(t, "FAKE_THREAD_ID", "FAKE_SESSION_ID")
	for _, tc := range []struct {
		name  string
		hints []string
		env   map[string]string
		want  string // "" means an error that names several ancestor leads
	}{
		{name: "no hint", want: ""},
		{name: "hint", hints: []string{"b"}, want: "b"},
		{name: "first live hint", hints: []string{"gone", "a", "b"}, want: "a"},
		{name: "hint that is no lead", hints: []string{"worker-own-id"}, want: ""},
		{name: "env thread", env: map[string]string{"FAKE_THREAD_ID": "b"}, want: "b"},
		{name: "env root thread", env: map[string]string{"FAKE_THREAD_ID": "sub", "FAKE_SESSION_ID": "a"}, want: "a"},
		{name: "hint before env", hints: []string{"a"}, env: map[string]string{"FAKE_THREAD_ID": "b"}, want: "a"},
		{name: "stale hint, env", hints: []string{"gone"}, env: map[string]string{"FAKE_THREAD_ID": "b"}, want: "b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := registry(t)
			for _, id := range []string{"a", "b"} {
				if err := r.RegisterLead(Lead{ID: id, Target: config.Target{Agent: "codex"}, PID: os.Getpid()}); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("FAKE_THREAD_ID", tc.env["FAKE_THREAD_ID"])
			t.Setenv("FAKE_SESSION_ID", tc.env["FAKE_SESSION_ID"])
			l, err := r.ResolveLead("", tc.hints...)
			if tc.want == "" {
				if err == nil || !strings.Contains(err.Error(), "several ancestor leads") {
					t.Fatalf("resolved %q, %v; want several ancestor leads", l.ID, err)
				}
				return
			}
			if err != nil || l.ID != tc.want {
				t.Fatalf("resolved %q, %v; want %q", l.ID, err, tc.want)
			}
		})
	}
}

// TestResolveLeadNonLeadHintWalks checks that a hint that names no lead, as
// a worker's own session ID, falls back to the walk, which finds the one lead
// of the caller's process.
func TestResolveLeadNonLeadHintWalks(t *testing.T) {
	withSessionEnv(t, "FAKE_THREAD_ID")
	t.Setenv("FAKE_THREAD_ID", "worker-own-id")
	r := registry(t)
	for _, l := range []Lead{
		{ID: "mine", Target: config.Target{Agent: "claude"}, PID: os.Getpid()},
		{ID: "other", Target: config.Target{Agent: "claude"}, PID: 0},
	} {
		if err := r.RegisterLead(l); err != nil {
			t.Fatal(err)
		}
	}
	if l, err := r.ResolveLead("", "worker-run-id"); err != nil || l.ID != "mine" {
		t.Fatalf("resolved %q, %v; want mine", l.ID, err)
	}
}

// TestResolveLeadEnvLeadBeforeHints checks that DUOLOG_LEAD stays before
// every hint.
func TestResolveLeadEnvLeadBeforeHints(t *testing.T) {
	r := registry(t)
	for _, id := range []string{"a", "b"} {
		if err := r.RegisterLead(Lead{ID: id, Target: config.Target{Agent: "codex"}, PID: os.Getpid()}); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DUOLOG_LEAD", "a")
	if l, err := r.ResolveLead("", "b"); err != nil || l.ID != "a" {
		t.Fatalf("resolved %q, %v; want a", l.ID, err)
	}
}

// TestSoleInProcessEndsOthers checks that a new lead of an agent that runs
// one session per process ends the other live leads of its process, and one
// of an agent whose process hosts several, as Codex's app-server does, ends
// none.
func TestSoleInProcessEndsOthers(t *testing.T) {
	for _, tc := range []struct {
		agent    string
		sole     bool
		oldLives bool
	}{
		{agent: "claude", sole: true, oldLives: false},
		{agent: "codex", sole: false, oldLives: true},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			r := registry(t)
			pid, other := os.Getpid(), 0
			start := time.Now()
			observe := func(id string, pid int, at time.Time) {
				t.Helper()
				p := pid
				if _, err := r.ObserveLead(LeadObservation{ID: id, Agent: tc.agent, PID: &p, SoleInProcess: tc.sole}, at); err != nil {
					t.Fatal(err)
				}
			}
			observe("old", pid, start)
			observe("elsewhere", other, start)
			observe("new", pid, start.Add(time.Second))
			if _, ok, err := r.Lead("old"); err != nil || ok != tc.oldLives {
				t.Fatalf("old lead live = %v (%v), want %v", ok, err, tc.oldLives)
			}
			for _, id := range []string{"new", "elsewhere"} {
				if _, ok, err := r.Lead(id); err != nil || !ok {
					t.Fatalf("lead %s ended (%v)", id, err)
				}
			}
		})
	}
}
