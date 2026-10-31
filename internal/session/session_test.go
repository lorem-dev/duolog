package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
)

func registry(t *testing.T) *Registry {
	t.Helper()
	r, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestTwoLeadsKeepSeparateWorkers(t *testing.T) {
	r := registry(t)
	for _, lead := range []string{"a", "b"} {
		if e := r.Record(Entry{ID: lead, Lead: lead, Agent: "codex", Role: "plan", Thread: "topic", ThreadID: lead}); e != nil {
			t.Fatal(e)
		}
	}
	for _, lead := range []string{"a", "b"} {
		x, ok, err := r.Lookup(lead, "codex", "plan", "topic")
		if err != nil || !ok || x.ThreadID != lead {
			t.Fatal(x, ok)
		}
	}
	if e := r.Close("a"); e != nil {
		t.Fatal(e)
	}
	if _, ok, err := r.Lookup("a", "codex", "plan", "topic"); err != nil || ok {
		t.Fatal("closed found")
	}
}
func TestUnavailableEndsAtReset(t *testing.T) {
	r := registry(t)
	for agent, until := range map[string]time.Time{"codex": time.Now().Add(-time.Second), "claude": {}} {
		if e := r.MarkUnavailable(agent, until); e != nil {
			t.Fatal(e)
		}
	}
	u, err := r.Unavailable()
	if err != nil {
		t.Fatal(err)
	}
	if u["codex"] || !u["claude"] {
		t.Fatal(u)
	}
}
func TestLeadRegistration(t *testing.T) {
	r := registry(t)
	l := Lead{ID: "abcdef123", Target: config.Target{Agent: "codex", Model: "terra", Effort: "medium"}, Mode: mode.Edits}
	if e := r.RegisterLead(l); e != nil {
		t.Fatal(e)
	}
	got, ok, err := r.Lead(l.ID)
	if err != nil || !ok || got != l || Address(got) != "codex/lead.abcdef" {
		t.Fatal(got, ok)
	}
	if CurrentLead([]string{"OTHER=x", "DUOLOG_LEAD=abcdef123"}) != l.ID {
		t.Fatal("env")
	}
	if Address(Lead{ID: "ab", Target: l.Target}) != "codex/lead.ab" {
		t.Fatal("short id")
	}
}
func TestConcurrentRecordsAreAtomic(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, e := Open(dir)
			if e == nil {
				e = r.Record(Entry{ID: fmt.Sprint(i), Lead: fmt.Sprint(i), Agent: "claude", Role: "plan", Thread: "t"})
			}
			if e != nil {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	r, e := Open(filepath.Clean(dir))
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 12; i++ {
		if _, ok, err := r.Lookup(fmt.Sprint(i), "claude", "plan", "t"); err != nil || !ok {
			t.Fatal(i)
		}
	}
}

func TestUnavailableWithoutResetEndsWithLead(t *testing.T) {
	r := registry(t)
	t.Setenv("DUOLOG_LEAD", "lead-a")
	if e := r.RegisterLead(Lead{ID: "lead-a"}); e != nil {
		t.Fatal(e)
	}
	if e := r.MarkUnavailable("codex", time.Time{}); e != nil {
		t.Fatal(e)
	}
	if e := r.MarkUnavailable("claude", time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	u, err := r.Unavailable()
	if err != nil {
		t.Fatal(err)
	}
	if !u["codex"] {
		t.Fatal("limit not retained for current session")
	}
	if e := r.Close("lead-a"); e != nil {
		t.Fatal(e)
	}
	u, err = r.Unavailable()
	if err != nil {
		t.Fatal(err)
	}
	if u["codex"] || !u["claude"] {
		t.Fatal(u)
	}
	if _, ok, err := r.Lead("lead-a"); err != nil || ok {
		t.Fatal("closed lead still registered")
	}
}

func TestRegistryReadErrorsAreReturned(t *testing.T) {
	for _, damage := range []string{"corrupt", "unreadable", "lock"} {
		t.Run(damage, func(t *testing.T) {
			r := registry(t)
			switch damage {
			case "corrupt":
				if err := os.WriteFile(filepath.Join(r.Dir, "sessions.json"), []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			case "unreadable":
				if err := os.Mkdir(filepath.Join(r.Dir, "sessions.json"), 0700); err != nil {
					t.Fatal(err)
				}
			case "lock":
				// A regular file as the parent guarantees a lock-open error on every OS.
				p := filepath.Join(t.TempDir(), "not-a-directory")
				if err := os.WriteFile(p, nil, 0600); err != nil {
					t.Fatal(err)
				}
				r.Dir = p

			}
			if _, _, err := r.Lookup("lead", "codex", "implement", "thread"); err == nil {
				t.Error("Lookup swallowed registry error")
			}
			if _, _, err := r.Lead("lead"); err == nil {
				t.Error("Lead swallowed registry error")
			}
			if _, err := r.Unavailable(); err == nil {
				t.Error("Unavailable swallowed registry error")
			}
		})
	}
}
