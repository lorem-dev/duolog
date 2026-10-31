package session

import (
	"fmt"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveLeadPrecedenceAndAmbiguity(t *testing.T) {
	r := registry(t)
	t.Setenv("DUOLOG_LEAD", "")
	if _, err := r.ResolveLead(""); err == nil {
		t.Fatal("resolved absent lead")
	}
	a := Lead{ID: "a", Target: config.Target{Agent: "claude"}, PID: 0}
	if err := r.RegisterLead(a); err != nil {
		t.Fatal(err)
	}
	if l, err := r.ResolveLead(""); err != nil || l.ID != "a" {
		t.Fatal(l, err)
	}
	b := Lead{ID: "b", Target: config.Target{Agent: "codex"}, PID: 0}
	if err := r.RegisterLead(b); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ResolveLead(""); err == nil {
		t.Fatal("resolved ambiguous leads")
	}
	t.Setenv("DUOLOG_LEAD", "b")
	if l, err := r.ResolveLead("a"); err != nil || l.ID != "a" {
		t.Fatal(l, err)
	}
	if l, err := r.ResolveLead(""); err != nil || l.ID != "b" {
		t.Fatal(l, err)
	}
	t.Setenv("DUOLOG_LEAD", "")
	a.PID = os.Getpid()
	if err := r.RegisterLead(a); err != nil {
		t.Fatal(err)
	}
	if l, err := r.ResolveLead(""); err != nil || l.ID != "a" {
		t.Fatal(l, err)
	}
	if _, err := r.ResolveLead("missing"); err == nil {
		t.Fatal("missing explicit lead fell back")
	}
}

func TestTranscriptBeyondFirstTailAndApprovalPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lead.jsonl")
	for _, approval := range []string{"never", "on-request", "untrusted", ""} {
		text := fmt.Sprintf("{\"type\":\"turn_context\",\"payload\":{\"model\":\"gpt-6-sol\",\"effort\":\"low\",\"approval_policy\":%q,\"sandbox_policy\":{\"type\":\"danger-full-access\"}}}\n", approval) + strings.Repeat("{\"type\":\"output\"}\n", 10000)
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := ReadLeadState("codex", path)
		want := mode.Ask
		if approval == "never" {
			want = mode.Full
		}
		if err != nil || got.Mode != want || got.Effort != "low" {
			t.Fatal(got, err)
		}
	}
}

func TestResumeClearsTombstone(t *testing.T) {
	r := registry(t)
	start := time.Now()
	full := mode.Full
	o := LeadObservation{ID: "resume", Agent: "claude", Mode: &full}
	if _, err := r.ObserveLead(o, start); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ObserveLead(LeadObservation{ID: o.ID, Agent: o.Agent, Ended: true}, start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ObserveLead(o, start); err == nil {
		t.Fatal("stale observation revived lead")
	}
	if _, err := r.ObserveLead(o, start.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := r.access(false, func(s *state) error {
		if s.Ended[o.ID] {
			t.Error("resume left tombstone")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
