package delegate

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/runner"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// The result line is the contract with the lead: it is all the lead reads of
// a worker. These goldens pin it byte for byte before the code moves. Run
// with -update to rewrite them after a change that is meant.
var updateGolden = flag.Bool("update", false, "rewrite the golden files")

// goldenDir is found when the test binary starts: the harness changes into a
// temporary folder, where a relative path would point elsewhere.
var goldenDir = func() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return filepath.Join(wd, "testdata", "golden")
}()

// checkGolden compares got with testdata/golden/<name>.golden. A golden holds
// the text and a trailing newline, so that a line that ends in a space still
// shows in a diff.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join(goldenDir, name+".golden")
	got += "\n"
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v; run with -update to write it", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from its golden.\n got: %q\nwant: %q", name, got, string(want))
	}
}

var taskID = regexp.MustCompile(`tasks/[^/\]\s]+`)

// goldenLine makes a line the same on every system: the repository folder
// becomes <TMP> and the task folder's generated name <TASK>.
func goldenLine(h *harness, line string) string {
	root := filepath.ToSlash(h.root)
	line = strings.ReplaceAll(line, root, "<TMP>")
	line = strings.ReplaceAll(line, h.root, "<TMP>")
	return taskID.ReplaceAllString(line, "tasks/<TASK>")
}

// record is the golden text of one outcome: the line, then what the lead gets
// beside it. The error is part of the contract, since a failed run has a line
// and an error.
//
// A run's line depends on the length of its generated task folder name and of
// the temporary folder, so its byte count and the exact place of a cut differ
// between runs. A cut line is recorded as its head, then "<cut text>", then
// everything from the cut marker on, and the golden says whether the line fits
// 512 bytes. The cut place itself is pinned by TestResultLineGolden, whose
// pointer has a fixed length.
func record(h *harness, o Outcome, err error) string {
	var b strings.Builder
	line := goldenLine(h, o.Line)
	if i := strings.Index(line, " ... [cut; "); i >= 0 {
		line = short(line, 40) + "<cut text>" + line[i:]
	}
	fmt.Fprintf(&b, "fits %d bytes: %t\nline: %s\n", maxLine, len(o.Line) <= maxLine, line)
	fmt.Fprintf(&b, "needs: %v\n", o.Needs)
	if err != nil {
		fmt.Fprintf(&b, "error: %s", goldenLine(h, err.Error()))
	} else {
		b.WriteString("error: none")
	}
	return b.String()
}

// Cases for resultLine alone. The pointer is a fixed string, so that the cut
// marker is the same everywhere.
func TestResultLineGolden(t *testing.T) {
	const pointer = "answer=<TASK>/answer.json"
	var files []string
	for i := range 40 {
		files = append(files, fmt.Sprintf("src/%02d%s.go", i, strings.Repeat("x", 30)))
	}
	// Two-byte letters and a three-byte one, so the cut at 512 bytes falls
	// inside a character unless it backs up to a start byte.
	multi := strings.Repeat("é€ü", 300)
	// The same, shifted by one byte, so both alignments of the cut are seen.
	multiShifted := "a" + multi
	cases := []struct {
		name                                   string
		done                                   bool
		needs, summary, files, warning, answer string
	}{
		{"done_summary_files", true, "", "Added the retry loop.", "a.go,b/c.go", "", pointer},
		{"done_with_decisions", true, " decisions=2", "Added the retry loop.", "a.go", "", pointer},
		{"done_warning", true, "", "Added the retry loop.", "a.go", "; rules are off; open /hooks", pointer},
		{"done_outside_warning", true, "", "Added the retry loop.", "a.go", "; outside=other/b.go,other/c.go; folder trust missing", pointer},
		{"needs_question", false, " needs=skill:figma,mcp:wiki", "Stopped: which API version?", "", "", pointer},
		{"not_done_no_needs", false, "", "Could not finish: the tests need a database.", "a.go", "", pointer},
		{"summary_with_newlines", true, "", "Line one.\n\n  Line two,\tindented.\r\nLine three.", "a.go", "", pointer},
		{"long_summary_ascii", true, "", strings.Repeat("word ", 300), "a.go", "", pointer},
		{"long_summary_multibyte", true, "", multi, "a.go", "", pointer},
		{"long_summary_multibyte_shifted", true, "", multiShifted, "a.go", "", pointer},
		{"long_summary_keeps_needs_and_warning", false, " needs=approval,LAST_NEED", strings.Repeat("word ", 300), "a.go", "; rules are off; open /hooks", pointer},
		{"long_files_cut_at_comma", true, " needs=approval", "Completed.", strings.Join(files, ","), "; rules are off; open /hooks; outside=other/b.go; some notice", pointer},
		{"warning_longer_than_line", true, "", "Completed.", "a.go,b.go", "; outside=" + strings.Repeat("other/y.go,", 60), pointer},
		{"single_long_file", true, "", "Completed.", strings.Repeat("z", 600), "", pointer},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resultLine(c.done, c.needs, c.summary, c.files, c.warning, c.answer)
			checkGolden(t, "line_"+c.name, fmt.Sprintf("line (%d bytes): %s", len(got), got))
		})
	}
}

// Cases through Run, with the harness's stand-in for the worker, so that the
// golden holds what the lead really reads: the router, the gate, the notices
// and the cut all run.
func TestRunLineGolden(t *testing.T) {
	long := strings.TrimSpace(strings.Repeat("refused ", 80))
	cases := []struct {
		name string
		set  func(t *testing.T, h *harness)
	}{
		{"run_done", func(t *testing.T, h *harness) {
			h.results = []runner.Result{{Done: true, Summary: "Added the retry loop.", Files: []string{"a.go", "b/c.go"}, Needs: []string{}}}
		}},
		{"run_done_with_decisions", func(t *testing.T, h *harness) {
			h.results = []runner.Result{{Done: true, Summary: "Added the retry loop.", Files: []string{"a.go"}, Needs: []string{}, Decisions: []string{"the library", "the file layout"}}}
		}},
		{"run_needs", func(t *testing.T, h *harness) {
			h.results = []runner.Result{{Done: false, Summary: "Stopped.", Files: []string{}, Needs: []string{"figma", "codegraph"}}}
		}},
		{"run_failed", func(t *testing.T, h *harness) {
			h.errs = []error{errors.New("worker exploded: exit status 2")}
		}},
		{"run_failed_long_error", func(t *testing.T, h *harness) {
			h.errs = []error{errors.New(long)}
		}},
		// The lead here is Claude, so a Codex worker that ends on quota has
		// no other agent to fall to: the task goes back to the lead.
		{"run_quota_to_lead", func(t *testing.T, h *harness) {
			h.limited()
		}},
		// The lead here is Codex, so the fallback is a Claude worker. It has
		// no Claude gate, so the line carries the hooks note too.
		{"run_quota_fallback", func(t *testing.T, h *harness) {
			h.claudeGate(false)
			h.limited()
			h.results = append(h.results, runner.Result{Done: true, Summary: "Completed by the other agent.", Files: []string{"a.go"}, Needs: []string{}})
		}},
		{"run_quota_fallback_fails", func(t *testing.T, h *harness) {
			h.limited()
			h.errs = append(h.errs, errors.New("fallback exploded"))
		}},
		{"run_interrupted", func(t *testing.T, h *harness) {
			h.results = []runner.Result{{Interrupted: true}}
			h.errs = []error{errors.New("context canceled")}
		}},
		{"run_overage_notice", func(t *testing.T, h *harness) {
			h.results = []runner.Result{{Done: true, Summary: "Completed.", Files: []string{}, Needs: []string{},
				Usage: runner.Usage{Input: 12, Output: 3}, UsageKnown: true, UsageComplete: true,
				Billing: quota.RunEvidence{Expected: true, Seen: true, UsingOverage: true}}}
		}},
		{"run_migration_notice_joined", func(t *testing.T, h *harness) {
			t.Setenv("OPENAI_API_KEY", "sk-test-value")
			unsetEnv(t, "ANTHROPIC_API_KEY")
			h.results = []runner.Result{{Done: true, Summary: "Completed.", Files: []string{"a.go"}, Needs: []string{}}}
		}},
		{"run_migration_notice_after_error", func(t *testing.T, h *harness) {
			t.Setenv("OPENAI_API_KEY", "sk-test-value")
			unsetEnv(t, "ANTHROPIC_API_KEY")
			h.errs = []error{errors.New("worker exploded")}
		}},
		{"run_migration_notice_cut_before_it", func(t *testing.T, h *harness) {
			t.Setenv("OPENAI_API_KEY", "sk-test-value")
			unsetEnv(t, "ANTHROPIC_API_KEY")
			h.errs = []error{errors.New(long)}
		}},
		{"run_overage_and_migration_notices", func(t *testing.T, h *harness) {
			t.Setenv("OPENAI_API_KEY", "sk-test-value")
			unsetEnv(t, "ANTHROPIC_API_KEY")
			h.results = []runner.Result{{Done: true, Summary: "Completed.", Files: []string{}, Needs: []string{},
				Usage: runner.Usage{Input: 12, Output: 3}, UsageKnown: true, UsageComplete: true,
				Billing: quota.RunEvidence{Expected: true, Seen: true, UsingOverage: true}}}
		}},
		{"run_long_summary_multibyte_cut", func(t *testing.T, h *harness) {
			h.results = []runner.Result{{Done: true, Summary: strings.Repeat("é€ü", 300), Files: []string{"a.go"}, Needs: []string{}}}
		}},
		{"run_long_summary_with_notice", func(t *testing.T, h *harness) {
			t.Setenv("OPENAI_API_KEY", "sk-test-value")
			unsetEnv(t, "ANTHROPIC_API_KEY")
			h.results = []runner.Result{{Done: true, Summary: strings.Repeat("é€ü", 300), Files: []string{"a.go"}, Needs: []string{}}}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clearRoutes(t)
			h := authHarness(t)
			if strings.HasPrefix(c.name, "run_quota_fallback") {
				h = newHarness(t)
			}
			c.set(t, h)
			o, err := h.run("implement")
			checkGolden(t, c.name, record(h, o, err))
		})
	}
}

// The migration line is a fixed text the notice cases above quote; pin it so
// that a change to its words shows in this file as well as in theirs.
func TestMigrationLineGolden(t *testing.T) {
	checkGolden(t, "migration_line_codex", workerenv.MigrationLine("codex", "OPENAI_API_KEY"))
}
