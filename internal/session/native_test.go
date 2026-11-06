package session

import (
	"os"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

func TestObservedNativeFollowsMode(t *testing.T) {
	r, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	edits, auto := mode.Edits, "auto"
	if err := r.RegisterLead(Lead{ID: "a", Target: config.Target{Agent: "claude"}, Mode: mode.Ask, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	// After the registration: Windows' clock can give it the same time, at
	// which a mode is lowered and a native cleared.
	now := time.Now().Add(time.Second)
	l, err := r.ObserveLead(LeadObservation{ID: "a", Agent: "claude", Mode: &edits, Native: &auto}, now)
	if err != nil || l.Native != "auto" || l.Mode != mode.Edits {
		t.Fatalf("%+v %v", l, err)
	}
	model := "opus"
	if l, _ = r.ObserveLead(LeadObservation{ID: "a", Agent: "claude", Model: &model}, now.Add(time.Second)); l.Native != "auto" {
		t.Fatalf("an observation without mode changed native: %+v", l)
	}
	if l, _ = r.ObserveLead(LeadObservation{ID: "a", Agent: "claude", Mode: &edits}, now.Add(2*time.Second)); l.Native != "" {
		t.Fatalf("a mode without native must clear it: %+v", l)
	}
}

func TestObservedNativeClearsAtEqualTimestamp(t *testing.T) {
	r, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	edits, auto, accept := mode.Edits, "auto", "acceptEdits"
	if err := r.RegisterLead(Lead{ID: "a", Target: config.Target{Agent: "claude"}, Mode: mode.Ask, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := r.ObserveLead(LeadObservation{ID: "a", Agent: "claude", Mode: &edits, Native: &auto}, now); err != nil {
		t.Fatal(err)
	}
	l, err := r.ObserveLead(LeadObservation{ID: "a", Agent: "claude", Mode: &edits, Native: &accept}, now)
	if err != nil || l.Native != "" {
		t.Fatalf("a different native at an equal timestamp must clear it: %+v %v", l, err)
	}
}
