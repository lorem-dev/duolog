package main

import (
	"bytes"
	"fmt"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/learning"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func classProposalJournal(t *testing.T, root, taskID string) *journal.Journal {
	t.Helper()
	j, err := journal.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		d := journal.Delegation{ID: fmt.Sprintf("u%d", i), Task: taskID, Step: i, Role: "review_final", Class: learning.Unclassified, RuleClass: learning.Unclassified, Done: true}
		if err := j.Append("delegations.ndjson", d); err != nil {
			t.Fatal(err)
		}
	}
	return j
}

func TestLearnApplyClass(t *testing.T) {
	root := learningRepo(t, true)
	classProposalJournal(t, root, "t1")
	path := filepath.Join(root, ".duolog", "config.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := executeBusCommand(t, "learn")
	if err != nil {
		t.Fatal(out, err)
	}
	for _, want := range []string{"no change to the learned order", "review_final_misc", "+", "unclassified delegations", "apply with: duolog learn --apply-class review_final_misc; decline with: duolog learn --decline-class review_final_misc"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q: %s", want, out)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("preview changed config", err)
	}
	afterInfo, err := os.Stat(path)
	if err != nil || !info.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatal("preview rewrote config", err)
	}
	if out, err := executeBusCommand(t, "learn", "--apply-class", "review_final_misc"); err != nil {
		t.Fatal(out, err)
	}
	cfg, err := config.Load(root)
	if err != nil || learning.Classify(cfg.Learning.Classify, "review_final", nil) != "review_final_misc" {
		t.Fatal(cfg, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".duolog", config.LearnedRoutingFile)); !os.IsNotExist(err) {
		t.Fatal("class apply wrote learned order", err)
	}
}

func TestLearnDeclineWritesJournalFile(t *testing.T) {
	root := learningRepo(t, true)
	j := classProposalJournal(t, root, "t1")
	learned := filepath.Join(root, ".duolog", config.LearnedRoutingFile)
	data := []byte("# preserve learned order\nladders: {}\n")
	if err := os.WriteFile(learned, data, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := executeBusCommand(t, "learn", "--decline-class", "review_final_misc"); err != nil {
		t.Fatal(out, err)
	}
	ds, err := journal.ReadAll[journal.Declined](j, "declined.ndjson")
	if err != nil || len(ds) != 1 || ds[0].Name != "review_final_misc" {
		t.Fatal(ds, err)
	}
	after, err := os.ReadFile(learned)
	if err != nil || !bytes.Equal(data, after) {
		t.Fatal("decline changed learned order", err)
	}
	out, err := executeBusCommand(t, "learn")
	if err != nil || strings.Contains(out, "review_final_misc") {
		t.Fatal(out, err)
	}
}

func TestLearnClassFlagsRefuseWorker(t *testing.T) {
	root := learningRepo(t, true)
	classProposalJournal(t, root, "t1")
	path := filepath.Join(root, ".duolog", "config.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DUOLOG_DEPTH", "1")
	for _, flag := range []string{"--apply-class", "--decline-class"} {
		_, err := executeBusCommand(t, "learn", flag, "review_final_misc")
		if err == nil || !strings.Contains(err.Error(), "workers cannot") {
			t.Fatal(flag, err)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("worker changed config", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".duolog", "journal", "declined.ndjson")); !os.IsNotExist(err) {
		t.Fatal("worker recorded decline", err)
	}
}

func TestLearnClassCollisionFlagsActOnCurrentGroup(t *testing.T) {
	root := learningRepo(t, true)
	j, err := journal.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range []struct {
		count   int
		feature string
	}{{7, "yes"}, {5, "no"}} {
		for i := 0; i < group.count; i++ {
			if err := j.Append("delegations.ndjson", journal.Delegation{Class: learning.Unclassified, Role: "review_final", Features: map[string]string{"exemplar": group.feature}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	readProposals := func() []learning.ClassProposal {
		cfg, err := config.Load(root)
		if err != nil {
			t.Fatal(err)
		}
		ds, err := journal.ReadAll[journal.Delegation](j, "delegations.ndjson")
		if err != nil {
			t.Fatal(err)
		}
		declined, err := declinedClasses(j)
		if err != nil {
			t.Fatal(err)
		}
		return learning.ProposeClasses(cfg, ds, declined)
	}
	ps := readProposals()
	if len(ps) != 1 || ps[0].Rule.When["exemplar"] != "yes" {
		t.Fatal(ps)
	}
	if out, err := executeBusCommand(t, "learn", "--decline-class", "review_final_misc"); err != nil {
		t.Fatal(out, err)
	}
	ps = readProposals()
	if len(ps) != 1 || ps[0].Rule.When["exemplar"] != "no" {
		t.Fatal(ps)
	}
	if out, err := executeBusCommand(t, "learn", "--apply-class", "review_final_misc"); err != nil {
		t.Fatal(out, err)
	}
	if ps := readProposals(); len(ps) != 0 {
		t.Fatal(ps)
	}
	cfg, err := config.Load(root)
	if err != nil || learning.Classify(cfg.Learning.Classify, "review_final", map[string]string{"exemplar": "no"}) != "review_final_misc" || learning.Classify(cfg.Learning.Classify, "review_final", map[string]string{"exemplar": "yes"}) == "review_final_misc" {
		t.Fatal(cfg, err)
	}
}

func TestLearnWorkerMayPreviewClassWithoutWritingProjectState(t *testing.T) {
	root := learningRepo(t, true)
	j := classProposalJournal(t, root, "t1")
	path := filepath.Join(root, ".duolog", "config.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DUOLOG_DEPTH", "1")
	out, err := executeBusCommand(t, "learn")
	if err != nil || !strings.Contains(out, "class proposal review_final_misc") {
		t.Fatal(out, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("worker preview changed config", err)
	}
	afterInfo, err := os.Stat(path)
	if err != nil || !info.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatal("worker preview rewrote config", err)
	}
	if _, err := os.Stat(filepath.Join(j.Dir, "declined.ndjson")); !os.IsNotExist(err) {
		t.Fatal("worker preview wrote declines", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".duolog", config.LearnedRoutingFile)); !os.IsNotExist(err) {
		t.Fatal("worker preview wrote learned routing", err)
	}
}

func TestLearnClassFlagsRejectNamesNotCurrentlyOffered(t *testing.T) {
	root := learningRepo(t, true)
	j := classProposalJournal(t, root, "t1")
	for _, flag := range []string{"--apply-class", "--decline-class"} {
		_, err := executeBusCommand(t, "learn", flag, "not-a-proposal")
		if err == nil || !strings.Contains(err.Error(), "not-a-proposal") {
			t.Fatal(flag, err)
		}
	}
	if _, err := os.Stat(filepath.Join(j.Dir, "declined.ndjson")); !os.IsNotExist(err) {
		t.Fatal("unknown proposal was declined", err)
	}
}

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

// With learning mode off, config.Load does not read the learned order, so an
// applied order routes nothing. learn says so in one line, with the setting
// that turns learning mode on, and still shows and saves the order.
func TestLearnSaysLearningIsOff(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		root := learningRepo(t, enabled)
		learnJournal(t, root, true)
		for _, args := range [][]string{{"learn"}, {"learn", "--apply"}} {
			out, err := executeBusCommand(t, args...)
			if err != nil || !strings.Contains(out, "+    order:") {
				t.Fatal(enabled, args, out, err)
			}
			if got := strings.Count(out, learningOffLine); enabled && got != 0 || !enabled && got != 1 {
				t.Fatalf("learning enabled %t, %v: %d off lines, output:\n%s", enabled, args, got, out)
			}
		}
		if _, err := os.Stat(filepath.Join(root, ".duolog", config.LearnedRoutingFile)); err != nil {
			t.Fatalf("learning enabled %t: the order was not saved: %v", enabled, err)
		}
	}
	if !strings.Contains(learningOffLine, "learning.enabled: true") || strings.Contains(learningOffLine, "\n") {
		t.Fatalf("the line does not say how to turn learning mode on: %q", learningOffLine)
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

// calibrationJournal records n tests tasks that terra failed and sol passed,
// with no ladder, as calibrate judge writes them.
func calibrationJournal(t *testing.T, root string, n int) {
	t.Helper()
	j, err := journal.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	for i := range n {
		for _, r := range []struct {
			entry  string
			passed bool
		}{{"codex/terra/medium", false}, {"codex/sol/medium", true}} {
			c := journal.CalibrationResult{Kind: "result", Time: now, Run: "cal-1", Task: fmt.Sprintf("t%d", i+1), Class: "tests",
				Entry: r.entry, Verification: "hidden", Passed: r.passed, Source: "calibration"}
			if err := j.Append(journal.CalibrationFile, c); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestLearnUsesCalibrationEvidence(t *testing.T) {
	root := learningRepo(t, true)
	calibrationJournal(t, root, 3)
	out, err := executeBusCommand(t, "learn")
	if err != nil || !strings.Contains(out, "calibration tasks 3") || !strings.Contains(out, "+    order:") {
		t.Fatal(out, err)
	}
	if out, err := executeBusCommand(t, "learn", "--apply"); err != nil {
		t.Fatal(out, err)
	}
	cfg, err := config.Load(root)
	if err != nil || len(cfg.Learning.Learned["tests"].Order) == 0 || cfg.Learning.Learned["tests"].Order[0] != "codex/sol/medium" {
		t.Fatal(cfg.Learning.Learned, err)
	}
}

func TestLearnSaysNoDifferenceFound(t *testing.T) {
	root := learningRepo(t, true)
	calibrationJournal(t, root, 2)
	out, err := executeBusCommand(t, "learn")
	if err != nil || !strings.Contains(out, "tests: no difference found, keeps the configured order") {
		t.Fatal(out, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".duolog", config.LearnedRoutingFile)); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestLearnSeedsFromProjectPart(t *testing.T) {
	root := learningRepo(t, true)
	sharedProjectPart(t, root)
	out, err := executeBusCommand(t, "learn")
	if err != nil || !strings.Contains(out, "+    order:") {
		t.Fatal(out, err)
	}
	if out, err := executeBusCommand(t, "learn", "--apply"); err != nil {
		t.Fatal(out, err)
	}
	cfg, err := config.Load(root)
	if err != nil || len(cfg.Learning.Learned["tests"].Order) == 0 || cfg.Learning.Learned["tests"].Order[0] != "codex/sol/medium" {
		t.Fatal(cfg.Learning.Learned, err)
	}

	// With a personal result the seed is ignored: one task both entries
	// passed separates nothing.
	other := learningRepo(t, true)
	sharedProjectPart(t, other)
	j, err := journal.Open(filepath.Join(other, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{"codex/terra/medium", "codex/sol/medium"} {
		c := journal.CalibrationResult{Kind: "result", Time: time.Now().UTC(), Run: "cal-1", Task: "t1", Class: "tests", Entry: entry,
			Verification: "hidden", Passed: true, Source: "calibration"}
		if err := j.Append(journal.CalibrationFile, c); err != nil {
			t.Fatal(err)
		}
	}
	out, err = executeBusCommand(t, "learn")
	if err != nil || strings.TrimSpace(out) != "no change to the learned order" {
		t.Fatal(out, err)
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
