package main

import (
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/journal"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func learnJournal(t *testing.T, root string, fail bool) {
	t.Helper()
	j, err := journal.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	d := journal.Delegation{ID: "d1", Task: "t1", Class: "debug", Ladder: "debug", Entry: "codex/terra/medium", Done: !fail}
	for _, r := range []struct {
		name string
		v    any
	}{{"delegations.ndjson", d}, {"tasks.ndjson", journal.Task{Kind: "finish", ID: "t1", Outcome: "accepted"}}} {
		if err := j.Append(r.name, r.v); err != nil {
			t.Fatal(err)
		}
	}
}
func TestLearnPrintsDiffAndApplyWrites(t *testing.T) {
	root := learningRepo(t, true)
	learnJournal(t, root, true)
	out, err := executeBusCommand(t, "learn")
	if err != nil || !strings.Contains(out, "+    order:") || !strings.Contains(out, "evidence:") || !strings.Contains(out, "apply with: duolog learn --apply") {
		t.Fatal(out, err)
	}
	path := filepath.Join(root, ".duolog", config.LearnedRoutingFile)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if out, err := executeBusCommand(t, "learn", "--apply"); err != nil {
		t.Fatal(out, err)
	}
	cfg, err := config.Load(root)
	if err != nil || len(cfg.Learning.Learned["debug"].Order) == 0 {
		t.Fatal(cfg, err)
	}
	if out, err := executeBusCommand(t, "learn", "--apply"); err != nil || !strings.Contains(out, "no change to the learned order") {
		t.Fatal(out, err)
	}
}
func TestLearnNoChange(t *testing.T) {
	root := learningRepo(t, true)
	for _, args := range [][]string{{"learn"}, {"learn", "--apply"}} {
		out, err := executeBusCommand(t, args...)
		if err != nil || strings.TrimSpace(out) != "no change to the learned order" {
			t.Fatal(out, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".duolog", config.LearnedRoutingFile)); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
func TestLearnStatsOnlyIsNoChange(t *testing.T) {
	root := learningRepo(t, true)
	learnJournal(t, root, false)
	out, err := executeBusCommand(t, "learn", "--apply")
	if err != nil || strings.TrimSpace(out) != "no change to the learned order" {
		t.Fatal(out, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".duolog", config.LearnedRoutingFile)); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
func TestLearnApplyRefusedForWorker(t *testing.T) {
	root := learningRepo(t, true)
	learnJournal(t, root, true)
	t.Setenv("DUOLOG_DEPTH", "1")
	_, err := executeBusCommand(t, "learn", "--apply")
	if err == nil || !strings.Contains(err.Error(), "workers cannot") {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".duolog", config.LearnedRoutingFile)); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
