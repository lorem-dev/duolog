package peer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
