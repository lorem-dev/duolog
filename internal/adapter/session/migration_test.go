package session

import (
	"sync"
	"testing"
	"time"
)

// Registries of several processes race for one lead's notice: one claim wins.
// Another key, or a new lead, gets a fresh notice, and an ended lead's notices
// go with it.
func TestClaimNoticeAtomicPerLead(t *testing.T) {
	dir := t.TempDir()
	const key = "migration:claude:ANTHROPIC_API_KEY"
	const racers = 8
	var wg sync.WaitGroup
	claims := make(chan bool, racers)
	errs := make(chan error, racers)
	for range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reg, err := Open(dir)
			if err != nil {
				errs <- err
				return
			}
			ok, err := reg.ClaimNotice("lead-a", key)
			if err != nil {
				errs <- err
				return
			}
			claims <- ok
		}()
	}
	wg.Wait()
	close(claims)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	won := 0
	for ok := range claims {
		if ok {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("%d of %d claims won, want 1", won, racers)
	}

	reg, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	claim := func(lead, key string, want bool) {
		t.Helper()
		ok, err := reg.ClaimNotice(lead, key)
		if err != nil || ok != want {
			t.Fatalf("ClaimNotice(%s, %s) = %t, %v; want %t", lead, key, ok, err, want)
		}
	}
	claim("lead-a", key, false)
	claim("lead-a", "migration:codex:OPENAI_API_KEY", true)
	claim("lead-b", key, true)
	claim("lead-b", key, false)

	if err := reg.RegisterLead(Lead{ID: "lead-b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.ObserveLead(LeadObservation{ID: "lead-b", Ended: true}, time.Now()); err != nil {
		t.Fatal(err)
	}
	var kept bool
	if err := reg.access(false, func(s *state) error { _, kept = s.Notices["lead-b"]; return nil }); err != nil {
		t.Fatal(err)
	}
	if kept {
		t.Fatal("an ended lead kept its notices")
	}
	claim("lead-a", key, false)
}
