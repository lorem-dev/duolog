package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/session"
	"time"
)

func learningRepo(t *testing.T, enabled bool) string {
	t.Helper()
	isolatedRouteConfig(t)
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("DUOLOG_LEAD", "")
	t.Setenv("DUOLOG_DEPTH", "")
	// The committer is a two-word name: calibration refuses a project part
	// that holds a history name in any field, and "test" is in go test.
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=Sample Tester", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatal(string(out), err)
		}
	}
	if enabled {
		if err := os.MkdirAll(filepath.Join(root, ".duolog"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".duolog", "config.yaml"), []byte("learning:\n  enabled: true\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterLead(session.Lead{ID: "task-lead", Target: config.Target{Agent: "claude", Model: "haiku"}, Mode: mode.Full, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestTaskStartOpensTask(t *testing.T) {
	root := learningRepo(t, true)
	head, _ := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	out, err := executeBusCommand(t, "task", "start", "fix", "login")
	if err != nil {
		t.Fatal(out, err)
	}
	if !regexp.MustCompile(`task t-\d{8}-\d{6}-[0-9a-f]{4} started at ` + strings.TrimSpace(string(head))[:7]).MatchString(out) {
		t.Fatal(out)
	}
}

func TestTaskStartWhenLearningOff(t *testing.T) {
	root := learningRepo(t, false)
	out, err := executeBusCommand(t, "task", "start")
	if err != nil || strings.TrimSpace(out) != "learning mode is off in .duolog/config.yaml; nothing recorded" {
		t.Fatal(out, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".duolog", "journal")); !os.IsNotExist(err) {
		t.Fatal("journal created", err)
	}
}

func TestTaskCommandRefusesWorker(t *testing.T) {
	learningRepo(t, true)
	t.Setenv("DUOLOG_DEPTH", "1")
	_, err := executeBusCommand(t, "task", "start")
	if err == nil || !strings.Contains(err.Error(), "workers cannot change the journal") {
		t.Fatal(err)
	}
}

func TestTaskStartNoCommitsYet(t *testing.T) {
	root := learningRepo(t, true)
	os.RemoveAll(filepath.Join(root, ".git"))
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	out, err := executeBusCommand(t, "task", "start")
	if err != nil || !strings.Contains(out, "no commits yet") {
		t.Fatal(out, err)
	}
}

func startedTask(t *testing.T, root, lead string) (*journal.Journal, journal.OpenTask) {
	t.Helper()
	j, err := journal.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	base, err := journal.Head(root)
	if err != nil {
		t.Fatal(err)
	}
	task, err := j.StartTask(lead, "fix", base, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return j, task
}
func TestFinishPrintsTableThenOffersThenQuestion(t *testing.T) {
	root := learningRepo(t, true)
	j, task := startedTask(t, root, "task-lead")
	d := journal.Delegation{ID: "d1", Task: task.ID, Step: 1, Lead: task.Lead, Class: "debug", Ladder: "debug", Role: "debug_hard", Entry: "codex/terra/medium", Done: true, USD: 0.2, Priced: true}
	if err := j.Append("delegations.ndjson", d); err != nil {
		t.Fatal(err)
	}
	out, err := executeBusCommand(t, "task", "finish")
	if err != nil {
		t.Fatal(out, err)
	}
	a, b, c := strings.Index(out, "STEP"), strings.Index(out, "Profiling offers"), strings.Index(out, "Ask the user for the outcome")
	if a < 0 || b <= a || c <= b {
		t.Fatal(out)
	}
	records, err := journal.ReadAll[journal.Task](j, "tasks.ndjson")
	if err != nil || len(records) != 1 {
		t.Fatal(records, err)
	}
	r := records[0]
	if r.Kind != "finish" || r.ID != task.ID || r.Final != task.Base || r.FinalTree == "" || len(r.Delegations) != 1 || r.Delegations[0] != d.ID || r.USD != 0.2 {
		t.Fatal(r)
	}
}
func TestFinishClosesOnlyThisLead(t *testing.T) {
	root := learningRepo(t, true)
	j, _ := startedTask(t, root, "task-lead")
	other, err := j.StartTask("other-lead", "other", "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if out, err := executeBusCommand(t, "task", "finish", "--lead", "task-lead"); err != nil {
		t.Fatal(out, err)
	}
	if _, ok, err := j.CurrentTask("task-lead"); err != nil || ok {
		t.Fatal(ok, err)
	}
	if got, ok, err := j.CurrentTask("other-lead"); err != nil || !ok || got.ID != other.ID {
		t.Fatal(got, ok, err)
	}
}
func TestFinishWithoutOpenTask(t *testing.T) {
	learningRepo(t, true)
	_, err := executeBusCommand(t, "task", "finish")
	if err == nil || !strings.Contains(err.Error(), "no open task for this lead") {
		t.Fatal(err)
	}
}

func TestFinishMentionsClassProposal(t *testing.T) {
	for _, decline := range []bool{false, true} {
		t.Run(map[bool]string{false: "proposed", true: "declined"}[decline], func(t *testing.T) {
			root := learningRepo(t, true)
			j, task := startedTask(t, root, "task-lead")
			classProposalJournal(t, root, task.ID)
			if decline {
				if err := j.Append("declined.ndjson", journal.Declined{Name: "review_final_misc"}); err != nil {
					t.Fatal(err)
				}
			}
			out, err := executeBusCommand(t, "task", "finish")
			if err != nil {
				t.Fatal(out, err)
			}
			at := strings.Index(out, "A new class proposal is ready: run duolog learn after the outcome.")
			if decline {
				if at >= 0 {
					t.Fatal(out)
				}
			} else if at < strings.Index(out, "Profiling offers") || at >= strings.Index(out, "Ask the user for the outcome") {
				t.Fatal(out)
			}
		})
	}
}
func TestWritingTaskCommandsRefuseWorker(t *testing.T) {
	root := learningRepo(t, true)
	j, task := startedTask(t, root, "task-lead")
	before, err := os.ReadFile(filepath.Join(j.Dir, "open.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DUOLOG_DEPTH", "1")
	for _, args := range [][]string{{"task", "finish"}, {"task", "outcome", task.ID, "accepted"}} {
		_, err := executeBusCommand(t, args...)
		if err == nil || err.Error() != "workers cannot change the journal" {
			t.Fatal(args, err)
		}
	}
	after, err := os.ReadFile(filepath.Join(j.Dir, "open.json"))
	if err != nil || string(after) != string(before) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(j.Dir, "tasks.ndjson")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
func TestOutcomeRecorded(t *testing.T) {
	root := learningRepo(t, true)
	j, task := startedTask(t, root, "task-lead")
	if out, err := executeBusCommand(t, "task", "finish"); err != nil {
		t.Fatal(out, err)
	}
	for _, outcome := range []string{"accepted", "small_fixes"} {
		if out, err := executeBusCommand(t, "task", "outcome", task.ID, outcome, "--note", "adjusted tests"); err != nil {
			t.Fatal(out, err)
		}
	}
	rs, err := journal.ReadAll[journal.Task](j, "tasks.ndjson")
	if err != nil || len(rs) != 3 {
		t.Fatal(rs, err)
	}
	r := rs[len(rs)-1]
	if r.Kind != "outcome" || r.Outcome != "small_fixes" || r.Note != "adjusted tests" || r.ID != task.ID {
		t.Fatal(r)
	}
}
func TestOutcomeRejectsUnknownValue(t *testing.T) {
	learningRepo(t, true)
	_, err := executeBusCommand(t, "task", "outcome", "t1", "fine")
	if err == nil || !strings.Contains(err.Error(), "accepted, small_fixes, reworked, dropped") {
		t.Fatal(err)
	}
}
func TestOutcomeForUnfinishedTask(t *testing.T) {
	root := learningRepo(t, true)
	_, task := startedTask(t, root, "task-lead")
	_, err := executeBusCommand(t, "task", "outcome", task.ID, "accepted")
	if err == nil || !strings.Contains(err.Error(), "is not finished") {
		t.Fatal(err)
	}
}

func TestTaskStartPrintsProtocol(t *testing.T) {
	root := learningRepo(t, true)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	out, err := executeBusCommand(t, "task", "start", "fix")
	if err != nil {
		t.Fatal(out, err)
	}
	for feature := range config.Features {
		if !strings.Contains(out, feature+"=") {
			t.Errorf("missing feature %s in %s", feature, out)
		}
	}
	var names []string
	for name := range cfg.Learning.Classes {
		names = append(names, name)
	}
	slices.Sort(names)
	previous := strings.Index(out, "Classes:\n")
	if previous < 0 {
		t.Fatal(out)
	}
	for _, name := range names {
		c := cfg.Learning.Classes[name]
		line := name + " (" + c.Priority + "): " + c.Description + "\n"
		at := strings.Index(out, line)
		if at <= previous {
			t.Errorf("missing or unsorted %q in %s", line, out)
		}
		previous = at
	}
	for _, want := range []string{"--escalate-from", "--use", "out_of_turns", "duolog task finish", "plan, then review_final", "then implement_complex"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	if lines := strings.SplitN(out, "\n", 2); len(lines) != 2 || !strings.HasPrefix(lines[1], "Learning mode routes each delegation by its class.") {
		t.Fatal(out)
	}
}
