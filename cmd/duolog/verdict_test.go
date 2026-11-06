package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/core/learn"
	"github.com/lorem-dev/duolog/internal/journal"
)

const verdictCriterion = "structure matches what was asked"

func verdictJournal(t *testing.T) *journal.Journal {
	t.Helper()
	root := learningRepo(t, true)
	j, err := journal.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	v := journal.Verdict{Kind: "verdict", ID: "v1", Class: "skeleton", Ladder: "skeleton", Original: "claude/opus/high", Profile: "codex/sol/high", Scores: []journal.Score{{Criterion: verdictCriterion, Original: 3, Profile: 2}, {Criterion: "other", Original: 3, Profile: 1}}}
	if err := j.Append("verdicts.ndjson", v); err != nil {
		t.Fatal(err)
	}
	return j
}

func readVerdicts(t *testing.T, j *journal.Journal) []journal.Verdict {
	t.Helper()
	vs, err := journal.ReadAll[journal.Verdict](j, "verdicts.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	return vs
}

func TestVerdictConfirmAndCorrect(t *testing.T) {
	j := verdictJournal(t)
	for _, args := range [][]string{{"verdict", "v1", "confirm"}, {"verdict", "v1", "correct", verdictCriterion + "=1/3", "--note", "adjusted structure"}} {
		if out, err := executeBusCommand(t, args...); err != nil {
			t.Fatal(out, err)
		}
	}
	vs := readVerdicts(t, j)
	if len(vs) != 3 || vs[1].Kind != "decision" || vs[1].ID != "v1" || vs[1].Decision != "confirm" || len(vs[1].Scores) != 0 || vs[2].Decision != "correct" || vs[2].Note != "adjusted structure" || len(vs[2].Scores) != 1 || vs[2].Scores[0].Original != 1 || vs[2].Scores[0].Profile != 3 {
		t.Fatal(vs)
	}
}

func TestVerdictCorrectUnknownCriterion(t *testing.T) {
	j := verdictJournal(t)
	_, err := executeBusCommand(t, "verdict", "v1", "correct", "unknown=1/2")
	if err == nil || !strings.Contains(err.Error(), "unknown") || len(readVerdicts(t, j)) != 1 {
		t.Fatal(err)
	}
}

func TestVerdictPartialCorrectionAllowed(t *testing.T) {
	j := verdictJournal(t)
	if out, err := executeBusCommand(t, "verdict", "v1", "confirm"); err != nil {
		t.Fatal(out, err)
	}
	if out, err := executeBusCommand(t, "verdict", "v1", "correct", verdictCriterion+"=1/3"); err != nil {
		t.Fatal(out, err)
	}
	vs := readVerdicts(t, j)
	if len(vs) != 3 || len(vs[2].Scores) != 1 {
		t.Fatal(vs)
	}
	// The existing aggregator overlays only this criterion. The second survives,
	// and the latest decision wins over an earlier confirm.
	cfg, err := configfile.Load(filepath.Dir(filepath.Dir(j.Dir)))
	if err != nil {
		t.Fatal(err)
	}
	stats := learn.Aggregate(cfg, nil, nil, vs)
	if stats["skeleton"]["claude/opus/high"].Quality != 2 || stats["skeleton"]["codex/sol/high"].Quality != 2 {
		t.Fatal(stats)
	}
	if out, err := executeBusCommand(t, "verdict", "v1", "confirm"); err != nil {
		t.Fatal(out, err)
	}
	stats = learn.Aggregate(cfg, nil, nil, readVerdicts(t, j))
	if stats["skeleton"]["claude/opus/high"].Quality != 3 || stats["skeleton"]["codex/sol/high"].Quality != 1.5 {
		t.Fatal(stats)
	}
}

func TestVerdictRefusesWorker(t *testing.T) {
	j := verdictJournal(t)
	path := filepath.Join(j.Dir, "verdicts.ndjson")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DUOLOG_DEPTH", "1")
	for _, action := range []string{"confirm", "correct"} {
		_, err := executeBusCommand(t, "verdict", "v1", action, verdictCriterion+"=1/3")
		if err == nil || !strings.Contains(err.Error(), "workers cannot") {
			t.Fatal(err)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("worker changed journal", err)
	}
}

func TestVerdictUnknownID(t *testing.T) {
	j := verdictJournal(t)
	_, err := executeBusCommand(t, "verdict", "missing", "confirm")
	if err == nil || !strings.Contains(err.Error(), "missing") || len(readVerdicts(t, j)) != 1 {
		t.Fatal(err)
	}
}

func TestVerdictRejectsInvalidCorrections(t *testing.T) {
	j := verdictJournal(t)
	for _, args := range [][]string{{"correct"}, {"confirm", verdictCriterion + "=1/2"}, {"correct", verdictCriterion + "=4/2"}, {"correct", verdictCriterion + "=1/-1"}, {"correct", verdictCriterion + "=one/2"}, {"correct", verdictCriterion + "=1/2/3"}, {"correct", verdictCriterion + "=1/2", verdictCriterion + "=2/3"}, {"unknown"}} {
		_, err := executeBusCommand(t, append([]string{"verdict", "v1"}, args...)...)
		if err == nil || len(readVerdicts(t, j)) != 1 {
			t.Fatal(args, err)
		}
	}
}
