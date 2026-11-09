package policy

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/core/mode"
)

func TestPreflightReportsEveryCommandThatIsNotAllowed(t *testing.T) {
	p := policy(t)
	work := t.TempDir()
	in := PreflightInput{Policy: p, Mode: mode.Edits, Role: "implement", Work: work, State: work}
	commands := []string{"go test ./...", "go run ./cmd/x", "uv run git push", "uv run mypy src"}
	got := Preflight(in, commands)
	if len(got) != 2 {
		t.Fatalf("got %+v, want two blocked commands", got)
	}
	if got[0].Command != "go run ./cmd/x" || got[0].Decision != Ask {
		t.Errorf("first = %+v, want go run asked", got[0])
	}
	if got[1].Command != "uv run git push" || got[1].Decision != Deny || got[1].Reason == "" || got[1].Rule == "" {
		t.Errorf("second = %+v, want git push denied with a reason and a rule", got[1])
	}
}

// The answer is the gate's: it follows the role and the mode, and never
// allows what the gate asks about.
func TestPreflightFollowsRoleAndMode(t *testing.T) {
	p := policy(t)
	work := t.TempDir()
	for _, tc := range []struct {
		role string
		m    mode.Mode
		want string
	}{
		{"implement", mode.Edits, ""},
		{"review_final", mode.Edits, Deny},
		{"implement", mode.Plan, Deny},
	} {
		in := PreflightInput{Policy: p, Mode: tc.m, Role: tc.role, Work: work, State: work}
		got := Preflight(in, []string{"go test ./..."})
		switch {
		case tc.want == "" && len(got) != 0:
			t.Errorf("%s in %s: blocked %+v", tc.role, tc.m, got)
		case tc.want != "" && (len(got) != 1 || got[0].Decision != tc.want):
			t.Errorf("%s in %s: got %+v, want %s", tc.role, tc.m, got, tc.want)
		}
	}
}

// The task's Read list reaches the judgment as it reaches the worker's gate: a
// read command on a listed path outside the work root is allowed, and asks
// without the list.
func TestPreflightUsesTheTasksReadList(t *testing.T) {
	f := newReadsFixture(t)
	in := PreflightInput{Policy: policy(t), Mode: mode.Edits, Role: "implement", Work: f.work, State: f.work}
	command := "cat " + f.word("docs/a.md")
	if got := Preflight(in, []string{command}); len(got) != 1 || got[0].Decision != Ask {
		t.Fatalf("without a Read list: %+v, want ask", got)
	}
	in.Reads = []string{f.path("docs")}
	if got := Preflight(in, []string{command}); len(got) != 0 {
		t.Errorf("with the folder in the Read list: %+v, want none", got)
	}
}

// Shell reaches the judgment: PowerShell has no POSIX variable expansion, so
// $HOME/x is not judged as a path built from one.
func TestPreflightPassesTheShell(t *testing.T) {
	work := t.TempDir()
	in := PreflightInput{Policy: policy(t), Mode: mode.Edits, Role: "implement", Work: work, State: work}
	command := "go test $HOME/x"
	posix := Preflight(in, []string{command})
	in.Shell = "powershell"
	shell := Preflight(in, []string{command})
	if len(posix) != 1 || len(shell) != 1 || posix[0].Rule == shell[0].Rule {
		t.Errorf("a POSIX shell gave %+v and PowerShell %+v; want the shell passed on", posix, shell)
	}
}

func TestPreflightAllowsNothingByItself(t *testing.T) {
	p := policy(t)
	work := t.TempDir()
	in := PreflightInput{Policy: p, Mode: mode.Edits, Role: "implement", Work: work, State: work}
	if got := Preflight(in, []string{"git push origin main", "make deploy"}); len(got) != 2 {
		t.Errorf("got %+v, want both commands blocked", got)
	}
	if got := Preflight(in, nil); len(got) != 0 {
		t.Errorf("got %+v for no commands", got)
	}
}
