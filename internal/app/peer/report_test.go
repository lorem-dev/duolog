package peer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/policy"
)

// A peer's report shows in status and wait only once the file exists.
func TestPeerStatusAndWaitShowReportPath(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.ready()
	want := ".duolog/tasks/" + e.ID + "/report.md"

	st, err := Status(r.ctx, e.ID)
	if err != nil || strings.Contains(st.Line, "report") {
		t.Fatal("no report written yet:", st.Line, err)
	}
	got, err := Wait(r.ctx, e.ID)
	if err != nil || got.Report != "" {
		t.Fatal("no report written yet:", got, err)
	}

	dir := filepath.Join(r.reg.Dir, "tasks", e.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "report.md"), []byte("findings\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	st, err = Status(r.ctx, e.ID)
	if err != nil || !strings.Contains(st.Line, "report "+want) {
		t.Fatal("status line lacks the report path:", st.Line, err)
	}
	got, err = Wait(r.ctx, e.ID)
	if err != nil || got.Report != want {
		t.Fatal("wait lacks the report path:", got, err)
	}
}

// finishTask sends a task to a ready peer, has the peer write report once the
// task is sent (when it is not empty), and waits until the task has finished.
func (r *rig) finishTask(e Entry, report string) Entry {
	r.t.Helper()
	path := r.task()
	script := baseScript()
	script["capture"] = screens(sendOK(path)...)
	r.panes(script)
	if err := Send(r.ctx, e.ID, path); err != nil {
		r.t.Fatal(err)
	}
	if report != "" {
		r.writeReport(e, report)
	}
	script = baseScript()
	script["capture"] = screens(codexIdle(""), codexIdle(""), codexIdle(""))
	r.panes(script)
	got, err := Wait(r.ctx, e.ID)
	if err != nil || got.State != Ready || !strings.Contains(got.Note, "task finished") {
		r.t.Fatalf("%+v %v", got, err)
	}
	return got
}

func (r *rig) writeReport(e Entry, text string) string {
	r.t.Helper()
	path := filepath.Join(r.root, filepath.FromSlash(policy.ReportPath(e.ID)))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		r.t.Fatal(err)
	}
	return path
}

// A finished task with a report: wait prints the path and the report's start.
func TestWaitShowsTheReportText(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.ready()
	got := r.finishTask(e, "PASS: nothing to fix\n\nDetails follow.\n")
	if got.Report != policy.ReportPath(e.ID) || got.Excerpt != "PASS: nothing to fix\n\nDetails follow." {
		t.Fatalf("%+v", got)
	}
	if strings.Contains(got.Note, "no report") {
		t.Fatal("the note says there is no report:", got.Note)
	}
}

// A report left from an earlier task is not the answer to the next one.
func TestSendRemovesTheOldReport(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.ready()
	old := r.writeReport(e, "old answer\n")
	got := r.finishTask(e, "")
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("the old report survived the send:", err)
	}
	if got.Report != "" || got.Excerpt != "" || !strings.Contains(got.Note, "no report") {
		t.Fatalf("a peer that wrote no report: %+v", got)
	}
}

// Status shows the report text of an idle peer too.
func TestStatusShowsTheReportText(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.ready()
	r.writeReport(e, "FAIL: one bug\n")
	st, err := Status(r.ctx, e.ID)
	if err != nil || !strings.Contains(st.Line, "report "+policy.ReportPath(e.ID)) || !strings.HasSuffix(st.Line, "\nFAIL: one bug") {
		t.Fatalf("%q %v", st.Line, err)
	}
}

// The excerpt is at most 15 lines and 1500 characters, cut at a line or rune.
func TestExcerptIsBounded(t *testing.T) {
	var lines []string
	for i := 1; i <= 40; i++ {
		lines = append(lines, "line "+strings.Repeat("x", i%3))
	}
	got := excerpt(strings.Join(lines, "\n"))
	if n := strings.Count(got, "\n") + 1; n > reportLines+1 || !strings.HasSuffix(got, "[report cut]") {
		t.Fatalf("%d lines: %q", n, got)
	}
	long := excerpt(strings.Repeat("é", 4000))
	if n := len([]rune(long)); n > reportRunes+len("\n[report cut]") || !strings.HasSuffix(long, "[report cut]") {
		t.Fatalf("%d runes", n)
	}
	if got := excerpt("short\n"); got != "short" {
		t.Fatalf("%q", got)
	}
	if got := excerpt(" \n\n"); got != "" {
		t.Fatalf("%q", got)
	}
}

// A report that is a link out of the checkout, or sits behind one, is no report.
func TestReportOutsideTheCheckoutIsIgnored(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.ready()
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(r.root, ".duolog", "tasks", e.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "report.md")); err != nil {
		t.Skip("no symlinks:", err)
	}
	if st, err := Status(r.ctx, e.ID); err != nil || strings.Contains(st.Line, "report") || strings.Contains(st.Line, "secret") {
		t.Fatalf("%q %v", st.Line, err)
	}
	if err := os.Remove(filepath.Join(dir, "report.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "report.md"), []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, dir); err != nil {
		t.Fatal(err)
	}
	if st, err := Status(r.ctx, e.ID); err != nil || strings.Contains(st.Line, "report") || strings.Contains(st.Line, "secret") {
		t.Fatalf("%q %v", st.Line, err)
	}
}

// A peer in a linked worktree writes its report where the gate lets it: under
// the worktree, whose work root differs from the project state folder. Status
// and wait find it there.
func TestPeerReportInLinkedWorktree(t *testing.T) {
	r := newRig(t, baseScript())
	r.ready()
	work := t.TempDir()
	git := filepath.Join(r.root, ".git")
	for _, dir := range []string{"objects", "refs/heads", "worktrees/feature"} {
		if err := os.MkdirAll(filepath.Join(git, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for path, text := range map[string]string{
		filepath.Join(git, "HEAD"):                        "ref: refs/heads/main\n",
		filepath.Join(git, "config"):                      "[core]\nrepositoryformatversion=0\nbare=false\n",
		filepath.Join(git, "worktrees/feature/HEAD"):      "ref: refs/heads/feature\n",
		filepath.Join(git, "worktrees/feature/commondir"): "../..\n",
		filepath.Join(git, "worktrees/feature/gitdir"):    filepath.Join(work, ".git") + "\n",
		filepath.Join(work, ".git"):                       "gitdir: " + filepath.Join(git, "worktrees/feature") + "\n",
	} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(work)
	e := r.open("implement")
	want := ".duolog/tasks/" + e.ID + "/report.md"
	// The state folder holds no report for this peer, and the worktree none yet.
	if st, err := Status(r.ctx, e.ID); err != nil || strings.Contains(st.Line, "report") {
		t.Fatal("no report written yet:", st.Line, err)
	}
	report := filepath.Join(work, filepath.FromSlash(want))
	if err := os.MkdirAll(filepath.Dir(report), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(report, []byte("findings\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Status(r.ctx, e.ID)
	if err != nil || !strings.Contains(st.Line, "report "+want) {
		t.Fatal("status line lacks the report path:", st.Line, err)
	}
	got, err := Wait(r.ctx, e.ID)
	if err != nil || got.Report != want {
		t.Fatal("wait lacks the report path:", got, err)
	}
	// From the main checkout the path is the worktree's, in full.
	t.Chdir(r.root)
	if st, err := Status(r.ctx, e.ID); err != nil || !strings.Contains(st.Line, "report "+report) {
		t.Fatal("status from the main checkout lacks the full path:", st.Line, err)
	}
}
