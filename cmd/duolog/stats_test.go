package main

import (
	"github.com/lorem-dev/duolog/internal/journal"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatsCommand(t *testing.T) {
	isolatedRouteConfig(t)
	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll(filepath.Join(root, ".duolog"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".duolog", "usage.ndjson"), []byte("{\"agent\":\"codex\",\"model\":\"luna\",\"input\":1000000}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := executeBusCommand(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "0.100000") {
		t.Fatal(out)
	}
}

func TestStatsClasses(t *testing.T) {
	root := learningRepo(t, true)
	j, err := journal.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	d := journal.Delegation{ID: "d1", Task: "t1", Class: "debug", Ladder: "debug", Entry: "codex/terra/medium", Done: true, Priced: true, USD: 0.2, Seconds: 10}
	if err := j.Append("delegations.ndjson", d); err != nil {
		t.Fatal(err)
	}
	if err := j.Append("tasks.ndjson", journal.Task{Kind: "outcome", ID: "t1", Outcome: "accepted"}); err != nil {
		t.Fatal(err)
	}
	out, err := executeBusCommand(t, "stats", "--classes")
	if err != nil {
		t.Fatal(out, err)
	}
	fields := strings.Fields(out)
	want := "LADDER ENTRY DELEGATIONS WEIGHT QUALITY FAILURES USD SECONDS debug codex/terra/medium 1 0.5 3.00 0 0.20 10"
	if strings.Join(fields, " ") != want {
		t.Fatal(out)
	}
}
func TestStatsClassesNoJournal(t *testing.T) {
	isolatedRouteConfig(t)
	t.Chdir(t.TempDir())
	out, err := executeBusCommand(t, "stats", "--classes")
	if err != nil || strings.TrimSpace(out) != "no journal; learning mode is off or has not run" {
		t.Fatal(out, err)
	}
	if _, err := os.Stat(".duolog"); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
