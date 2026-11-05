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

// A Claude record with cache writes shows them beside input and cached, and
// costs more than an older record of the same tokens without them.
func TestStatsShowsCacheWrites(t *testing.T) {
	isolatedRouteConfig(t)
	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll(filepath.Join(root, ".duolog"), 0755); err != nil {
		t.Fatal(err)
	}
	records := `{"role":"review","agent":"claude","model":"opus","input":1000000,"cached":0,"cache_write":1000000,"output":0}` + "\n" +
		`{"role":"implement","agent":"claude","model":"opus","input":1000000,"cached":0,"output":0}` + "\n"
	if err := os.WriteFile(filepath.Join(root, ".duolog", "usage.ndjson"), []byte(records), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := executeBusCommand(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out, "\n")
	if !strings.Contains(lines[0], "CACHED  CACHE WRITE  OUTPUT") {
		t.Fatal(out)
	}
	for _, want := range [][]string{{"implement", "1000000", "0", "0", "0", "4.000000"}, {"review", "1000000", "0", "1000000", "0", "5.000000"}} {
		found := false
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) == 8 && fields[0] == want[0] && strings.Join(fields[3:], " ") == strings.Join(want[1:], " ") {
				found = true
			}
		}
		if !found {
			t.Fatalf("no row %v in\n%s", want, out)
		}
	}
	if !strings.Contains(out, "Total estimated USD: 9.000000") {
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
