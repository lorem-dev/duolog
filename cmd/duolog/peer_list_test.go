package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/peer"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/testbin"
)

var oldPeerStates = map[string]bool{
	"starting": true, "updating": true, "question": true, "ready": true,
	"limited": true, "exited": true, "closed": true,
}

// recordListPeer records one claude peer of the test lead.
func recordListPeer(t *testing.T, root string) {
	t.Helper()
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Record(session.Entry{ID: "peer-list", Lead: "lead-1", Agent: "claude", Role: "plan", Block: "block:peer", RexSession: "session:old", Task: ".duolog/task.md"}); err != nil {
		t.Fatal(err)
	}
}

func TestPeerListPlainIsTwoFields(t *testing.T) {
	clearModelMarkers(t)
	asTerminal(t, true)
	tests := []struct {
		name    string
		capture testbin.Scenario
		wait    *testbin.Scenario
		state   string
	}{
		{"working", testbin.Scenario{Stdout: "✻ Thinking… (3s · esc to interrupt)\n" + claudeIdleScreen()}, nil, "starting"},
		{"question with spaces", testbin.Scenario{Stdout: startupFixture(t, "claude-model-switch-question.txt")}, nil, "question"},
		{"exited", testbin.Scenario{Stdout: claudeIdleScreen()}, &testbin.Scenario{Exit: 3, Stdout: `{"exit_code": 3}`}, "exited"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, _ := peerProject(t, tt.capture)
			if tt.wait != nil {
				testbin.Install(t, "rex", testbin.Scenario{Exit: 99, Stderr: "unexpected rex command", Responses: map[string]testbin.Scenario{
					"block inspect": {Stdout: `{"block":{"window_id":"window:lead"}}`},
					"wait":          *tt.wait,
					"capture":       tt.capture,
				}})
			}
			recordListPeer(t, root)
			out, err := executeBusCommand(t, "peer", "list")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := listBody(t, out)
			lines := strings.Split(strings.TrimSpace(body), "\n")
			if len(lines) != 1 {
				t.Fatalf("%q", out)
			}
			for _, line := range lines {
				f := strings.Fields(line)
				if len(f) != 2 || f[1] != tt.state || !oldPeerStates[f[1]] {
					t.Fatalf("plain line %q", line)
				}
			}
			out, err = executeBusCommand(t, "peer", "list", "--json")
			var peers []struct {
				State string `json:"state"`
			}
			if err != nil || json.Unmarshal([]byte(out), &peers) != nil || len(peers) != 1 || !oldPeerStates[peers[0].State] {
				t.Fatalf("%s %v", out, err)
			}
		})
	}
}

// listBody is the output of `peer list` without its last line, the load line,
// and that line. The load line is the last line of a list that prints one.
func listBody(t *testing.T, out string) (body, load string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[len(lines)-1], "duolog peers: ") {
		t.Fatalf("no load line at the end of %q", out)
	}
	return strings.Join(lines[:len(lines)-1], "\n"), lines[len(lines)-1]
}

func TestPeerListEndsWithTheLoadLine(t *testing.T) {
	clearModelMarkers(t)
	asTerminal(t, true)
	root, _ := peerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	recordListPeer(t, root)
	out, err := executeBusCommand(t, "peer", "list")
	if err != nil {
		t.Fatal(err)
	}
	body, line := listBody(t, out)
	if body != "peer-list ready" {
		t.Fatalf("body %q", body)
	}
	for _, want := range []string{"claude 1 running (1 idle) of 3", "codex 0 of 3", "idle: peer-list"} {
		if !strings.Contains(line, want) {
			t.Fatalf("load line %q lacks %q", line, want)
		}
	}
	// The line reads no quota, and no read is cached: the window is unknown.
	if strings.Contains(line, "5h") {
		t.Fatalf("load line %q has a window without a read", line)
	}
	// Another lead's item is not this lead's queue.
	queue := filepath.Join(root, ".duolog", "peer-queue.ndjson")
	if err := os.WriteFile(queue, []byte(`{"id":"q-other","lead":"other-lead","state":"queued"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = executeBusCommand(t, "peer", "list")
	if _, line = listBody(t, out); err != nil || strings.Contains(line, "queue") && !strings.Contains(line, "queue add") {
		t.Fatalf("another lead's item counted: %q %v", out, err)
	}
	// This lead's queued item is counted, with no advice: the hooks run it.
	if err := os.WriteFile(filepath.Join(root, "task.md"), []byte("## Goal\nx\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := executeBusCommand(t, "peer", "queue", "add", "implement", "--cwd", root, "--task", "task.md"); err != nil {
		t.Fatalf("%q %v", out, err)
	}
	out, err = executeBusCommand(t, "peer", "list")
	if _, line = listBody(t, out); err != nil || !strings.Contains(line, "; queue 1") || strings.Contains(line, "sessions free") {
		t.Fatalf("%q %v", out, err)
	}
	// JSON stays a plain list.
	out, err = executeBusCommand(t, "peer", "list", "--json")
	var peers []map[string]any
	if err != nil || json.Unmarshal([]byte(out), &peers) != nil {
		t.Fatalf("%s %v", out, err)
	}
}

// restartRex installs the fake rex of a restarted server: the old session is
// not listed and its panes are not found. The lead's process ended with it,
// so its entries are closed, and it came back under the same id.
func restartRex(t *testing.T, root string) (rex string) {
	t.Helper()
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Close("lead-1"); err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterLead(session.Lead{ID: "lead-1", Target: config.Target{Agent: "codex", Model: "sol", Effort: "high"}, Mode: mode.Ask, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REX_SESSION", "session:new")
	// Real Rex answers a block in a session that no longer exists like this.
	// Only the old session gets it; any other call finds the pane.
	notFound := testbin.Scenario{Exit: 3, Stderr: `Error: block target "block:peer" not found: session scope: session "session:old" not found`}
	return testbin.Install(t, "rex", testbin.Scenario{Exit: 99, Stderr: "unexpected rex command", Responses: map[string]testbin.Scenario{
		"session list":                 {Stdout: `{"sessions":[{"session_id":"session:new","label":"lead"}]}`},
		"block inspect":                {Stdout: `{"block":{"id":"block:peer","window_id":"window:lead"}}`},
		"block close":                  {},
		"block inspect -s session:old": notFound,
		"block close -s session:old":   notFound,
	}})
}

func TestPeerListAfterRexRestartShowsGonePeers(t *testing.T) {
	clearModelMarkers(t)
	asTerminal(t, true)
	root, _ := peerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	recordListPeer(t, root)
	restartRex(t, root)
	out, err := executeBusCommand(t, "peer", "list")
	if body, _ := listBody(t, out); err != nil || body != "peer-list gone" {
		t.Fatalf("%q %v", out, err)
	}
	out, err = executeBusCommand(t, "peer", "list", "--long")
	body, _ := listBody(t, out)
	lines := strings.Split(body, "\n")
	if err != nil || len(lines) != 2 || !strings.Contains(lines[1], "gone") {
		t.Fatalf("%q %v", out, err)
	}
	out, err = executeBusCommand(t, "peer", "list", "--json")
	var peers []struct{ ID, State string }
	if err != nil || json.Unmarshal([]byte(out), &peers) != nil || len(peers) != 1 || peers[0].State != "gone" {
		t.Fatalf("%s %v", out, err)
	}
}

func TestPeerCloseGonePeerSaysSoAndAsksRexForNoClose(t *testing.T) {
	clearModelMarkers(t)
	asTerminal(t, true)
	root, _ := peerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	recordListPeer(t, root)
	rex := restartRex(t, root)
	out, err := executeBusCommand(t, "peer", "close", "peer-list")
	if err != nil || !strings.Contains(out, "peer-list closed; its pane was already gone") {
		t.Fatalf("%q %v", out, err)
	}
	for _, call := range rexCalls(t, rex) {
		if len(call) >= 2 && call[0] == "block" && call[1] == "close" {
			t.Fatalf("rex was asked to close a pane that does not exist: %v", call)
		}
	}
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if entries, _ := reg.Entries(); len(entries) != 0 {
		t.Fatalf("record stays: %+v", entries)
	}
	if out, err = executeBusCommand(t, "peer", "list"); err != nil {
		t.Fatal(err)
	}
	if body, _ := listBody(t, out); body != "" {
		t.Fatalf("%q", out)
	}
}

func TestPeerListLong(t *testing.T) {
	clearModelMarkers(t)
	asTerminal(t, true)
	root, _ := peerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	recordListPeer(t, root)
	out, err := executeBusCommand(t, "peer", "list", "--long")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := listBody(t, out)
	lines := strings.Split(body, "\n")
	if len(lines) != 2 {
		t.Fatalf("%q", out)
	}
	if got := strings.Join(strings.Fields(lines[0]), " "); got != "ID ROLE AGENT STATE TASK LAST TAB" {
		t.Fatalf("header %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "peer-list") {
		t.Fatalf("row %q", lines[1])
	}
	for _, want := range []string{"plan", "claude", "ready", ".duolog/task.md", "duolog | claude-plan"} {
		if !strings.Contains(lines[1], want) {
			t.Fatalf("row %q lacks %q", lines[1], want)
		}
	}
	if strings.Contains(out, "Try \"how does") {
		t.Fatalf("screen text in %q", out)
	}
}

func TestPeerListJSONAddsFields(t *testing.T) {
	clearModelMarkers(t)
	asTerminal(t, true)
	root, _ := peerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	recordListPeer(t, root)
	out, err := executeBusCommand(t, "peer", "list", "--json")
	var peers []map[string]any
	if err != nil || json.Unmarshal([]byte(out), &peers) != nil || len(peers) != 1 {
		t.Fatalf("%s %v", out, err)
	}
	for _, key := range []string{"id", "state", "working", "role", "agent", "tab", "work_root"} {
		if _, ok := peers[0][key]; !ok {
			t.Fatalf("no %q in %s", key, out)
		}
	}
	if peers[0]["state"] != "ready" {
		t.Fatalf("%s", out)
	}
}

func TestLastText(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		last *peer.LastEvent
		want string
	}{
		{"nil", nil, "-"},
		{"unknown start", &peer.LastEvent{Kind: "question"}, "question ? ago"},
		{"minutes", &peer.LastEvent{Kind: "task", At: now.Add(-4 * time.Minute)}, "task 4m ago"},
		{"seconds", &peer.LastEvent{Kind: "record", At: now.Add(-30 * time.Second)}, "record <1m ago"},
		{"hours", &peer.LastEvent{Kind: "task", At: now.Add(-3 * time.Hour)}, "task 3h ago"},
		{"days", &peer.LastEvent{Kind: "task", At: now.Add(-49 * time.Hour)}, "task 2d ago"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lastText(tt.last, now); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPeerListLongAndJSONConflict(t *testing.T) {
	clearModelMarkers(t)
	asTerminal(t, true)
	root, rex := peerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	recordListPeer(t, root)
	_, err := executeBusCommand(t, "peer", "list", "--long", "--json")
	if err == nil || !strings.Contains(err.Error(), "--long and --json cannot be used together") {
		t.Fatalf("%v", err)
	}
	if n := len(rexCalls(t, rex)); n != 0 {
		t.Fatalf("%d rex calls", n)
	}
}
