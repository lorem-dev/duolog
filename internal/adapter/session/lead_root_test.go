package session

import (
	"os"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/mode"
)

func TestLeadRootObservationOrdering(t *testing.T) {
	r, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, newer := t.TempDir(), t.TempDir()
	now := time.Now()
	pid := os.Getpid()
	for _, o := range []struct {
		root *string
		at   time.Time
	}{
		{&first, now}, {&newer, now.Add(2 * time.Second)},
		{&first, now.Add(time.Second)}, {nil, now.Add(3 * time.Second)},
	} {
		got, err := r.ObserveLead(LeadObservation{ID: "lead", Agent: "fake", Root: o.root, PID: &pid}, o.at)
		if err != nil {
			t.Fatal(err)
		}
		if o.at.After(now) && got.Root != newer {
			t.Fatalf("root = %q", got.Root)
		}
	}
	if err := r.SetMode("lead", mode.Plan); err != nil {
		t.Fatal(err)
	}
	got, _, err := r.EffectiveMode("lead")
	if err != nil || got.Root != newer {
		t.Fatalf("mode change: %+v %v", got, err)
	}
	if err := r.RegisterLead(got); err != nil {
		t.Fatal(err)
	}
	got, _, err = r.Lead("lead")
	if err != nil || got.Root != newer {
		t.Fatalf("registration: %+v %v", got, err)
	}
	if err := r.RegisterLead(Lead{ID: "registered", Root: newer, PID: pid}); err != nil {
		t.Fatal(err)
	}
	got, _, err = r.Lead("registered")
	if err != nil || got.Root != newer {
		t.Fatalf("initial registration: %+v %v", got, err)
	}
}
