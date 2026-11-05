package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/delegate"
	"github.com/lorem-dev/duolog/internal/peer"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/telemetry"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// peerTelemetryProject is a peer project with a fake rex and collection on in
// its temporary home. It returns the project root, a task file and the
// telemetry root.
func peerTelemetryProject(t *testing.T) (root, taskPath, telemetryRoot string) {
	t.Helper()
	root, _ = peerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	home := os.Getenv("HOME")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("DUOLOG_TELEMETRY", "")
	var err error
	if telemetryRoot, err = telemetry.ResolveRoot(telemetryLocation()); err != nil || !strings.HasPrefix(telemetryRoot, home) {
		t.Fatalf("telemetry root %s outside the temporary home: %v", telemetryRoot, err)
	}
	taskPath = filepath.Join(root, "task.md")
	body := "## Goal\nx\n\n## Read\n- a.go\n\n## Write\n- a.go\n\n## Limits\nnone\n\n## Check\nnone\n"
	if err := os.WriteFile(taskPath, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return root, taskPath, telemetryRoot
}

// peerRex installs a fake rex whose pane shows the screens in turn.
func peerRex(t *testing.T, screens ...string) {
	t.Helper()
	testbin.Install(t, "rex", testbin.Scenario{Exit: 99, Stderr: "unexpected rex command", Responses: map[string]testbin.Scenario{
		"new":           {Stdout: peerSplitReply},
		"window new":    {Stdout: peerSplitReply},
		"kill":          {},
		"block inspect": {Stdout: `{"block":{"window_id":"window:lead"}}`},
		"wait":          {Exit: 1, Stderr: "Error: wait timed out after 500ms: context deadline exceeded"},
		"capture":       sequence(screens...),
		"send":          {},
		"send-key":      {},
		"block close":   {},
		"block ls":      {Stdout: `{"blocks":[{"id":"block:peer"}]}`},
	}})
}

// claudeAtWork is a Claude pane at work on a task.
func claudeAtWork() string {
	return "✻ Channelling… (3s · esc to interrupt)\n" + claudeIdleScreen()
}

// claudeHolding is a Claude input line that holds the task line.
func claudeHolding(text string) string {
	rule := strings.Repeat("─", 64)
	return "\n" + rule + "\n❯ " + text + "\n" + rule + "\n  ⏸ manual mode on · ? for shortcuts\n"
}

// peerEvents reads this project's peer and run events.
func peerEvents(t *testing.T, root string) ([]telemetry.Peer, []telemetry.Run) {
	t.Helper()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	s, err := telemetry.Open(telemetryOptions(cfg, root))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	events, err := s.Read(telemetry.Query{})
	if err != nil {
		t.Fatal(err)
	}
	var peers []telemetry.Peer
	var runs []telemetry.Run
	for _, e := range events {
		switch x := e.(type) {
		case telemetry.Peer:
			peers = append(peers, x)
		case telemetry.Run:
			runs = append(runs, x)
		}
	}
	return peers, runs
}

func peerOps(events []telemetry.Peer) []string {
	var out []string
	for _, e := range events {
		out = append(out, e.Op+" "+e.Agent+" "+e.Outcome)
	}
	return out
}

func peerLine(t *testing.T, args ...string) string {
	t.Helper()
	out, err := executeBusCommand(t, args...)
	if err != nil {
		t.Fatalf("%v: %q %v", args, out, err)
	}
	return strings.TrimSpace(out)
}

func TestTelemetryPeerOperations(t *testing.T) {
	t.Run("lifecycle and the run", func(t *testing.T) {
		root, taskPath, telemetryRoot := peerTelemetryProject(t)
		peerRex(t, claudeIdleScreen())
		id, _, _ := strings.Cut(peerLine(t, "peer", "open", "plan"), " ")
		// The live peer is reused: a second open, no second pane.
		if again, _, _ := strings.Cut(peerLine(t, "peer", "open", "plan"), " "); again != id {
			t.Fatalf("not reused: %s %s", again, id)
		}
		if peers, runs := peerEvents(t, root); len(runs) != 0 || len(peers) != 2 {
			t.Fatalf("opening is no run: %+v %+v", peers, runs)
		}
		line := "Read task.md and do it. Do not call rex. Reply \"done\"."
		peerRex(t, claudeIdleScreen(), claudeIdleScreen(), claudeHolding(line), claudeAtWork())
		if out := peerLine(t, "peer", "send", id, taskPath); out != id+" sent "+taskPath {
			t.Fatal(out)
		}
		if _, runs := peerEvents(t, root); len(runs) != 0 {
			t.Fatalf("a sent task is no finished run: %+v", runs)
		}
		peerRex(t, claudeAtWork(), claudeIdleScreen(), claudeIdleScreen())
		if out := peerLine(t, "peer", "wait", id); !strings.Contains(out, "task finished") {
			t.Fatal(out)
		}
		peerLine(t, "peer", "close", id)
		agent := "claude"
		reg, err := session.Open(filepath.Join(root, ".duolog"))
		if err != nil {
			t.Fatal(err)
		}
		if removed, _ := reg.PeerRemoved(id); !removed {
			t.Fatal("not closed")
		}
		peers, runs := peerEvents(t, root)
		if got, want := peerOps(peers), []string{"open " + agent + " done", "open " + agent + " done", "send " + agent + " done", "close " + agent + " done"}; !slices.Equal(got, want) {
			t.Fatalf("peer events %q, want %q", got, want)
		}
		for _, p := range peers {
			if p.Secs < 0 || p.Secs > 60 || p.Type != "peer" {
				t.Fatalf("peer event %+v", p)
			}
		}
		// The task is one run, placed on the peer, apart from its lifecycle.
		if len(runs) != 1 {
			t.Fatalf("runs: %+v", runs)
		}
		r := runs[0]
		if r.Placement != "peer" || r.Agent != agent || r.Role != "plan" || r.Outcome != "done" || r.Err != "none" || r.Attempt != 1 || r.Run == (telemetry.ID{}) || r.UsageKnown || r.Secs > 60 {
			t.Fatalf("run %+v", r)
		}
		// No screen, answer, peer id, pane, task or path reaches telemetry.
		text := telemetryText(t, telemetryRoot)
		for _, leak := range []string{id, "peer-", "block:", "session:", "sess", "task.md", "Read ", "Channelling", "manual mode", "Try ", root, filepath.ToSlash(root)} {
			if strings.Contains(text, leak) {
				t.Fatalf("%q reached telemetry: %s", leak, text)
			}
		}
	})
	t.Run("failures", func(t *testing.T) {
		root, taskPath, _ := peerTelemetryProject(t)
		// Before any pane: an unknown peer to send to or close.
		if _, err := executeBusCommand(t, "peer", "send", "peer-nosuch", taskPath); err == nil {
			t.Fatal("sent to no peer")
		}
		if _, err := executeBusCommand(t, "peer", "close", "peer-nosuch"); err == nil {
			t.Fatal("closed no peer")
		}
		// Rex refuses the pane after routing chose the agent.
		testbin.Install(t, "rex", testbin.Scenario{Exit: 2, Stderr: "rex is down"})
		if _, err := executeBusCommand(t, "peer", "open", "plan"); err == nil {
			t.Fatal("opened without rex")
		}
		// Outside Rex the open fails before it knows an agent.
		t.Setenv("REX_SESSION", "")
		if _, err := executeBusCommand(t, "peer", "open", "plan"); err == nil {
			t.Fatal("opened outside rex")
		}
		t.Setenv("REX_SESSION", "sess")
		// A peer at work refuses a task.
		peerRex(t, claudeIdleScreen())
		id, _, _ := strings.Cut(peerLine(t, "peer", "open", "plan"), " ")
		peerRex(t, claudeAtWork())
		if _, err := executeBusCommand(t, "peer", "send", id, taskPath); err == nil {
			t.Fatal("sent to a busy peer")
		}
		// A task that is closed unfinished is a run that was interrupted.
		line := "Read task.md and do it. Do not call rex. Reply \"done\"."
		peerRex(t, claudeIdleScreen(), claudeIdleScreen(), claudeHolding(line), claudeAtWork())
		peerLine(t, "peer", "send", id, taskPath)
		peerLine(t, "peer", "close", id)
		peers, runs := peerEvents(t, root)
		want := []string{"send other failed", "close other failed", "open claude failed", "open other failed", "open claude done", "send claude failed", "send claude done", "close claude done"}
		if got := peerOps(peers); !slices.Equal(got, want) {
			t.Fatalf("peer events %q, want %q", got, want)
		}
		if len(runs) != 1 || runs[0].Placement != "peer" || runs[0].Outcome != "interrupted" {
			t.Fatalf("runs: %+v", runs)
		}
	})
	// A caller of the library, not a peer command, gets the same callbacks.
	t.Run("library callbacks", func(t *testing.T) {
		root, _, telemetryRoot := peerTelemetryProject(t)
		peerRex(t, claudeIdleScreen())
		var ops []peer.Operation
		var attempts []delegate.Attempt
		ctx := peer.WithOptions(context.Background(), peer.Options{Root: root, Lead: "lead-1",
			Observe:        func(o peer.Operation) { ops = append(ops, o) },
			ObserveAttempt: func(a delegate.Attempt) { attempts = append(attempts, a) }})
		e, err := peer.Open(ctx, "plan")
		if err != nil || e.State != peer.Ready {
			t.Fatal(e, err)
		}
		if err := peer.Close(ctx, e.ID); err != nil {
			t.Fatal(err)
		}
		if len(ops) != 2 || ops[0].Op != "open" || ops[0].Outcome != "done" || ops[0].Agent != "claude" || ops[0].Duration <= 0 || ops[1].Op != "close" || len(attempts) != 0 {
			t.Fatalf("%+v %+v", ops, attempts)
		}
		// The library writes nothing itself: the command's observer does.
		if _, err := os.Stat(telemetryRoot); !os.IsNotExist(err) {
			t.Fatalf("the peer package wrote telemetry: %v", err)
		}
	})
	t.Run("sink failure", func(t *testing.T) {
		_, taskPath, telemetryRoot := peerTelemetryProject(t)
		if err := os.MkdirAll(filepath.Dir(telemetryRoot), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(telemetryRoot, nil, 0600); err != nil {
			t.Fatal(err)
		}
		peerRex(t, claudeIdleScreen())
		opened := peerLine(t, "peer", "open", "plan")
		id, state, _ := strings.Cut(opened, " ")
		if state != "ready" {
			t.Fatal(opened)
		}
		line := "Read task.md and do it. Do not call rex. Reply \"done\"."
		peerRex(t, claudeIdleScreen(), claudeIdleScreen(), claudeHolding(line), claudeAtWork())
		if out := peerLine(t, "peer", "send", id, taskPath); out != id+" sent "+taskPath {
			t.Fatal(out)
		}
		if out := peerLine(t, "peer", "close", id); out != id+" closed" {
			t.Fatal(out)
		}
		// The observers take a store that is missing or closed.
		closed, err := telemetry.Open(telemetry.Options{Location: telemetryLocation()})
		if err == nil {
			_ = closed.Close()
			observePeer(closed)(peer.Operation{Op: "open", Agent: "claude", Outcome: "done", Duration: time.Second})
		}
		if observePeer(nil) != nil {
			t.Fatal("an observer without a store")
		}
	})
}
