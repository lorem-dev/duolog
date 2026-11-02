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
