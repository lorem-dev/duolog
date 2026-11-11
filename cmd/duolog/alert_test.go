package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// notices records what a fake Notify was asked to show.
type notices struct {
	mu   sync.Mutex
	got  [][2]string
	sent chan struct{}
}

func newNotices() *notices { return &notices{sent: make(chan struct{}, 16)} }

func (n *notices) notify(title, message string) {
	n.mu.Lock()
	n.got = append(n.got, [2]string{title, message})
	n.mu.Unlock()
	n.sent <- struct{}{}
}

func (n *notices) all() [][2]string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([][2]string(nil), n.got...)
}

// A person ask shows the person a desktop notice with its number and what it
// asks; the lead answers its own asks, and lead.notify false turns it off.
func TestGateNotifiesPersonAsk(t *testing.T) {
	for _, tc := range []struct {
		name, command, config string
		want                  bool
	}{
		{"person", "cat ../outside.txt", "", true},
		{"lead", "custom-command", "", false},
		{"off", "cat ../outside.txt", "lead:\n  notify: false\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, root := gateSetup(t, mode.Ask)
			shortAskWait(t)
			if tc.config != "" {
				writeUserConfig(t, tc.config)
			}
			b, err := bus.Open(filepath.Join(root, ".duolog"), config.Wire{MaxLineBytes: 512, MaxSummaryBytes: 200})
			if err != nil {
				t.Fatal(err)
			}
			for range 6 {
				if _, err := b.Send(bus.Message{From: "codex/lead.lead-1", To: "codex/worker", K: "status", S: "earlier"}); err != nil {
					t.Fatal(err)
				}
			}
			n := newNotices()
			testDeps(t).Notify = n.notify
			runGate(t, tc.command)
			asks := busAsks(t, root)
			if len(asks) != 1 || asks[0].N != 7 || asks[0].P != (tc.name != "lead") {
				t.Fatalf("asks %+v", asks)
			}
			if tc.want {
				select {
				case <-n.sent:
				case <-time.After(5 * time.Second):
					t.Fatal("no notice for a person ask")
				}
			}
			time.Sleep(100 * time.Millisecond)
			got := n.all()
			if !tc.want {
				if len(got) != 0 {
					t.Fatalf("notified %q", got)
				}
				return
			}
			want := [2]string{"duolog", fmt.Sprintf("ask 7 waits for you: %s", asks[0].S)}
			if len(got) != 1 || got[0] != want {
				t.Fatalf("notified %q, want %q", got, want)
			}
		})
	}
}

// A detached run that ends needing the person, failed or handed back tells
// the person on the desktop; a done run is told by the lead's notice and its
// Stop alert only. lead.notify false tells nothing.
func TestDetachedRunNotifies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		out    delegate.Outcome
		err    error
		config string
		want   string
	}{
		{"failed", delegate.Outcome{}, errors.New("worker crashed"), "", "run d-abc123 (review) failed"},
		{"needs", delegate.Outcome{Needs: []string{"mcp:x"}}, nil, "", "run d-abc123 (review) needs input"},
		{"run_here", delegate.Outcome{RunHere: &delegate.Placement{Where: "self", Task: "t.md"}}, nil, "", "run d-abc123 (review) run here"},
		{"done", delegate.Outcome{}, nil, "", ""},
		{"off", delegate.Outcome{}, errors.New("worker crashed"), "lead:\n  notify: false\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolatedRouteConfig(t)
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, ".duolog", "delegations"), 0o700); err != nil {
				t.Fatal(err)
			}
			if tc.config != "" {
				writeUserConfig(t, tc.config)
			}
			path := filepath.Join(root, ".duolog", "delegations", "d-abc123.json")
			base := delegate.Record{ID: "d-abc123", Role: "review", WorkRoot: root, State: "running", Started: time.Now().UTC()}
			if err := delegate.WriteRecord(path, base); err != nil {
				t.Fatal(err)
			}
			n := newNotices()
			writeDelegateResult(io.Discard, path, "review", "task.md", tc.out, tc.err, n.notify)
			var rec delegate.Record
			if data, err := os.ReadFile(path); err != nil || json.Unmarshal(data, &rec) != nil || rec.State == "running" {
				t.Fatalf("record not written: %s %v", data, err)
			}
			got := n.all()
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("notified %q", got)
				}
				return
			}
			if len(got) != 1 || got[0] != [2]string{"duolog", tc.want} {
				t.Fatalf("notified %q, want %q", got, tc.want)
			}
		})
	}
}
