package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/bus"
)

// statusLeadAddress is the bus address of the lead peerProject registers.
const statusLeadAddress = "codex/lead.lead-1"

func writeStatusBus(t *testing.T, root string, ms ...bus.Message) {
	t.Helper()
	var data []byte
	for _, m := range ms {
		line, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		data = append(append(data, line...), '\n')
	}
	if err := os.WriteFile(filepath.Join(root, ".duolog", "bus.ndjson"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func statusAsk(n int64, to, summary string, person bool) bus.Message {
	return bus.Message{N: n, T: time.Now().UTC().Format(time.RFC3339Nano), From: "claude/worker", To: to, K: "ask", S: summary, P: person}
}

func writeStatusRecord(t *testing.T, root string, r delegate.Record) string {
	t.Helper()
	path := filepath.Join(delegate.RecordDir(filepath.Join(root, ".duolog")), r.ID+".json")
	if err := delegate.WriteRecord(path, r); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMCPStatusListsPeersAsksDelegations(t *testing.T) {
	root, _ := peerProject(t, sequence(claudeIdleScreen(), claudeIdleScreen(), claudeIdleScreen()))
	opened, err := executeBusCommand(t, "peer", "open", "plan")
	if err != nil {
		t.Fatal(err)
	}
	peerID, _, _ := strings.Cut(strings.TrimSpace(opened), " ")
	writeStatusBus(t, root,
		statusAsk(1, statusLeadAddress, "run go test ./...", false),
		statusAsk(2, statusLeadAddress, "write outside the tree", true),
	)
	started := time.Now().UTC().Add(-time.Minute)
	path := writeStatusRecord(t, root, delegate.Record{ID: "d-aaaaaa", Role: "implement", Lead: "lead-1",
		TaskPath: ".duolog/tasks/d-aaaaaa/task.md", State: "done", Line: "ok", Started: started, Ended: started.Add(time.Second)})

	got, err := testDeps(t).mcpStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Peers) != 1 || got.Peers[0].ID != peerID || got.Peers[0].Role != "plan" {
		t.Fatalf("peers = %+v, want %s", got.Peers, peerID)
	}
	if len(got.OpenAsks) != 2 {
		t.Fatalf("asks = %+v", got.OpenAsks)
	}
	if a := got.OpenAsks[0]; a.N != 1 || a.From != "claude/worker" || a.Summary != "run go test ./..." || a.PersonOnly {
		t.Fatalf("first ask = %+v", a)
	}
	if a := got.OpenAsks[1]; a.N != 2 || !a.PersonOnly {
		t.Fatalf("second ask = %+v", a)
	}
	if len(got.Delegations) != 1 {
		t.Fatalf("delegations = %+v", got.Delegations)
	}
	if d := got.Delegations[0]; d.TaskPath != ".duolog/tasks/d-aaaaaa/task.md" || d.Role != "implement" || d.State != "done" || d.AnswerPath != path {
		t.Fatalf("delegation = %+v", d)
	}
}

func TestMCPStatusOnlyThisLeadsAsks(t *testing.T) {
	root, _ := peerProject(t, sequence(claudeIdleScreen()))
	writeStatusBus(t, root,
		statusAsk(1, statusLeadAddress, "mine", false),
		statusAsk(2, "claude/lead.other1", "another lead's", false),
	)
	writeStatusRecord(t, root, delegate.Record{ID: "d-aaaaaa", Role: "implement", Lead: "lead-1", TaskPath: "a", State: "done", Started: time.Now().UTC()})
	writeStatusRecord(t, root, delegate.Record{ID: "d-bbbbbb", Role: "implement", Lead: "other-lead", TaskPath: "b", State: "done", Started: time.Now().UTC()})
	got, err := testDeps(t).mcpStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.OpenAsks) != 1 || got.OpenAsks[0].N != 1 {
		t.Fatalf("asks = %+v, want only ask 1", got.OpenAsks)
	}
	if len(got.Delegations) != 1 || got.Delegations[0].TaskPath != "a" {
		t.Fatalf("delegations = %+v, want only this lead's", got.Delegations)
	}
}

func TestMCPStatusMarksLostDelegation(t *testing.T) {
	root, _ := peerProject(t, sequence(claudeIdleScreen()))
	// A pid no process holds: a running record with it reads as lost.
	writeStatusRecord(t, root, delegate.Record{ID: "d-aaaaaa", Role: "implement", Lead: "lead-1", TaskPath: "a",
		State: "running", PID: 1 << 30, Started: time.Now().UTC()})
	writeStatusRecord(t, root, delegate.Record{ID: "d-bbbbbb", Role: "review", Lead: "lead-1", TaskPath: "b",
		State: "running", PID: os.Getpid(), Started: time.Now().UTC().Add(-time.Second)})
	got, err := testDeps(t).mcpStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, d := range got.Delegations {
		states[d.TaskPath] = d.State
	}
	if states["a"] != "lost" || states["b"] != "running" {
		t.Fatalf("states = %v", states)
	}
}

func TestMCPStatusCapsDelegations(t *testing.T) {
	root, _ := peerProject(t, sequence(claudeIdleScreen()))
	base := time.Now().UTC().Add(-time.Hour)
	for i := range 25 {
		writeStatusRecord(t, root, delegate.Record{ID: fmt.Sprintf("d-%06d", i), Role: "implement", Lead: "lead-1",
			TaskPath: fmt.Sprintf("t%02d", i), State: "done", Started: base.Add(time.Duration(i) * time.Second)})
	}
	got, err := testDeps(t).mcpStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Delegations) != 20 {
		t.Fatalf("%d delegations, want 20", len(got.Delegations))
	}
	if got.Delegations[0].TaskPath != "t24" || got.Delegations[19].TaskPath != "t05" {
		t.Fatalf("not the newest 20, newest first: %s .. %s", got.Delegations[0].TaskPath, got.Delegations[19].TaskPath)
	}
}

func TestMCPStatusEmptyProjectHasEmptyLists(t *testing.T) {
	peerProject(t, sequence(claudeIdleScreen()))
	got, err := testDeps(t).mcpStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(got)
	for _, key := range []string{`"peers":[]`, `"open_asks":[]`, `"delegations":[]`} {
		if !strings.Contains(string(data), key) {
			t.Fatalf("%s lacks %s", data, key)
		}
	}
}
