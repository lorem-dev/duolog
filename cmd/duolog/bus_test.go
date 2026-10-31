package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/bus"
)

func executeBusCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestBusCommands(t *testing.T) {
	isolatedRouteConfig(t)
	root := t.TempDir()
	t.Chdir(root)
	output, err := executeBusCommand(t, "send", "--from", "claude/worker", "--to", "codex/lead.3fa9c1", "--kind", "status", "--thread", "thread-1", "--ref", "answer.json", "ready <tag>&")
	if err != nil || output != "1\n" {
		t.Fatalf("send = %q, %v", output, err)
	}
	stored, err := os.ReadFile(filepath.Join(root, ".duolog", "bus.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	var m bus.Message
	if err := json.Unmarshal(stored, &m); err != nil {
		t.Fatal(err)
	}
	if m.From != "claude/worker" || m.To != "codex/lead.3fa9c1" || m.K != "status" || m.Th != "thread-1" || m.Ref != "answer.json" || m.S != "ready <tag>&" {
		t.Fatalf("message = %+v", m)
	}
	output, err = executeBusCommand(t, "inbox", "--reader", m.To)
	if err != nil || output != string(stored) {
		t.Fatalf("inbox = %q, %v; want %q", output, err, stored)
	}
	if output, err := executeBusCommand(t, "inbox", "--reader", m.To); err != nil || output != "" {
		t.Fatalf("second inbox = %q, %v", output, err)
	}
	if err := os.WriteFile(filepath.Join(root, ".duolog", "config.yaml"), []byte("wire:\n  max_summary_bytes: 3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := executeBusCommand(t, "send", "--from", "writer", "--to", "reader", "--kind", "status", "four"); err == nil || !strings.Contains(err.Error(), "duolog put") {
		t.Fatalf("send ignored configured limit: %v", err)
	}
}

func TestBusCommandArguments(t *testing.T) {
	isolatedRouteConfig(t)
	t.Chdir(t.TempDir())
	for _, args := range [][]string{
		{"send", "summary"},
		{"send", "--from", "writer", "--to", "reader", "--kind", "status"},
		{"inbox"},
		{"inbox", "--reader", "reader", "extra"},
	} {
		if _, err := executeBusCommand(t, args...); err == nil {
			t.Errorf("%v: expected argument error", args)
		}
	}
}
