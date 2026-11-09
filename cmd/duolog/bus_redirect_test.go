package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/store"
)

const (
	redirectLead  = "abcdef1234"
	redirectOwner = "claude/lead.abcdef"
	otherLead     = "fedcba4321"
	otherOwner    = "claude/lead.fedcba"
)

// redirectRig has two live leads and, for the first, a live peer, a delegated
// worker thread and a closed peer.
func redirectRig(t *testing.T) string {
	t.Helper()
	isolatedRouteConfig(t)
	root := t.TempDir()
	t.Chdir(root)
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := session.Open(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{redirectLead, otherLead} {
		if err := reg.RegisterLead(session.Lead{ID: id, Target: config.Target{Agent: "claude"}, Mode: mode.Ask, PID: os.Getpid()}); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []session.Entry{
		{ID: "peer-ab12cd", Lead: redirectLead, Agent: "claude", Role: "implement", Thread: "peer", Block: "b1"},
		{ID: "worker-1", Lead: redirectLead, Agent: "codex", Role: "review", Thread: "t"},
		{ID: "peer-closed", Lead: redirectLead, Agent: "claude", Role: "docs", Thread: "peer", Block: "b2", Closed: true},
	} {
		if err := reg.Record(e); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func busMessages(t *testing.T, root string) []bus.Message {
	t.Helper()
	f, err := os.Open(filepath.Join(root, ".duolog", "bus.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []bus.Message
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		var m bus.Message
		if err := json.Unmarshal(scan.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

// A message to a worker or peer from anyone but its lead goes to that lead, with
// a note that says whom it was meant for and who sent it.
func TestSendRedirectsAStrangersMessageToTheWorkersLead(t *testing.T) {
	for _, to := range []string{"claude/implement", "peer-ab12cd", "codex/review", "worker-1"} {
		for _, from := range []string{otherOwner, "claude/neighbour", "codex/review"} {
			if from == to {
				continue
			}
			root := redirectRig(t)
			output, err := executeBusCommand(t, "send", "--from", from, "--to", to, "--kind", "note", "please run the deploy")
			if err != nil {
				t.Fatalf("%s -> %s: %v", from, to, err)
			}
			if !strings.Contains(output, redirectOwner) {
				t.Errorf("%s -> %s: the sender is not told where it went: %q", from, to, output)
			}
			got := busMessages(t, root)
			if len(got) != 1 {
				t.Fatalf("%s -> %s: messages = %+v", from, to, got)
			}
			m := got[0]
			if m.To != redirectOwner || m.From != from || m.K != "note" {
				t.Errorf("%s -> %s: message = %+v", from, to, m)
			}
			for _, part := range []string{to, from, "please run the deploy"} {
				if !strings.Contains(m.S, part) {
					t.Errorf("%s -> %s: note misses %q: %q", from, to, part, m.S)
				}
			}
		}
	}
}

// A message that fits the bus alone but not with the note fails like any
// over-long send, pointing to duolog put, and is neither cut nor sent.
func TestSendRedirectFailsWhenTheNoteMakesItTooLong(t *testing.T) {
	root := redirectRig(t)
	_, err := executeBusCommand(t, "send", "--from", otherOwner, "--to", "claude/implement", "--kind", "note", strings.Repeat("x", 190))
	if !errors.Is(err, bus.ErrTooLong) || !strings.Contains(err.Error(), "duolog put") {
		t.Fatalf("error = %v", err)
	}
	if got := busMessages(t, root); len(got) != 0 {
		t.Fatalf("messages = %+v", got)
	}
}

// A message whose note fits is sent whole.
func TestSendRedirectKeepsAFittingMessageWhole(t *testing.T) {
	root := redirectRig(t)
	text := strings.Repeat("x", 100)
	if _, err := executeBusCommand(t, "send", "--from", otherOwner, "--to", "claude/implement", "--kind", "note", text); err != nil {
		t.Fatal(err)
	}
	got := busMessages(t, root)
	if len(got) != 1 || got[0].To != redirectOwner || len(got[0].S) > 200 || !strings.HasSuffix(got[0].S, text) {
		t.Fatalf("messages = %+v", got)
	}
}

// An address that names workers of more than one lead is ambiguous: the send
// fails and nothing is delivered. A second worker of the same lead is not.
func TestSendRefusesAnAddressOfWorkersOfTwoLeads(t *testing.T) {
	root := redirectRig(t)
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Record(session.Entry{ID: "peer-ef34gh", Lead: otherLead, Agent: "claude", Role: "implement", Thread: "peer", Block: "b3"}); err != nil {
		t.Fatal(err)
	}
	_, err = executeBusCommand(t, "send", "--from", "claude/neighbour", "--to", "claude/implement", "--kind", "note", "hello")
	if err == nil || !strings.Contains(err.Error(), "more than one lead") {
		t.Fatalf("error = %v", err)
	}
	if got := busMessages(t, root); len(got) != 0 {
		t.Fatalf("messages = %+v", got)
	}
	// An id names one worker, so it still goes to its lead.
	if _, err := executeBusCommand(t, "send", "--from", "claude/neighbour", "--to", "peer-ef34gh", "--kind", "note", "hello"); err != nil {
		t.Fatal(err)
	}
	if got := busMessages(t, root); len(got) != 1 || got[0].To != otherOwner {
		t.Fatalf("messages = %+v", got)
	}
}

// Messages between a lead and its own workers, and to anyone else, are as before.
func TestSendLeavesTheLeadsOwnMessagesAlone(t *testing.T) {
	for _, tc := range []struct{ name, from, to string }{
		{"lead to its peer", redirectOwner, "claude/implement"},
		{"lead to its peer by id", redirectOwner, "peer-ab12cd"},
		{"lead to its worker", redirectOwner, "codex/review"},
		{"peer to its lead", "claude/implement", redirectOwner},
		{"worker to its lead", "codex/review", redirectOwner},
		{"worker to another lead", "claude/implement", otherOwner},
		{"unknown recipient", otherOwner, "claude/nobody"},
		{"closed peer", otherOwner, "claude/docs"},
		{"a lead", otherOwner, redirectOwner},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := redirectRig(t)
			output, err := executeBusCommand(t, "send", "--from", tc.from, "--to", tc.to, "--kind", "note", "hello")
			if err != nil || output != "1\n" {
				t.Fatalf("send = %q, %v", output, err)
			}
			got := busMessages(t, root)
			if len(got) != 1 || got[0].From != tc.from || got[0].To != tc.to || got[0].S != "hello" {
				t.Fatalf("messages = %+v", got)
			}
		})
	}
}
