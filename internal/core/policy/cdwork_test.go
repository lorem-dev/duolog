package policy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
)

// A cd into the folder a line already runs in changes nothing, so the rest of
// the line is judged alone. Any other cd keeps the line as it is.
func TestDropCdIntoCwd(t *testing.T) {
	work := pathutil.Resolved(t.TempDir())
	if err := os.Mkdir(filepath.Join(work, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	w := filepath.ToSlash(work)
	for _, tc := range []struct{ command, want string }{
		{"cd " + w + " && task ue:build", "task ue:build"},
		{"cd '" + w + "' && task ue:build", "task ue:build"},
		{`cd "` + w + `/" && go test ./...`, "go test ./..."},
		{"cd . && go test ./...", "go test ./..."},
		{"  cd   " + w + "   &&   go vet ./...", "go vet ./..."},
		// Another folder, an expansion, another operator or no rest: as it is.
		{"cd " + w + "/sub && go test ./...", ""},
		{"cd .. && go test ./...", ""},
		{"cd $HOME && go test ./...", ""},
		{"cd ~ && go test ./...", ""},
		{"cd " + w + "; go test ./...", ""},
		{"cd " + w + " || go test ./...", ""},
		{"cd " + w + " &&", ""},
		{"cd " + w, ""},
		{"cdx " + w + " && go test ./...", ""},
		{`cd "` + w + `"x && go test ./...`, ""},
	} {
		want := tc.want
		if want == "" {
			want = tc.command
		}
		if got := DropCdIntoCwd(tc.command, work, ""); got != want {
			t.Errorf("%q: %q, want %q", tc.command, got, want)
		}
	}
	if got := DropCdIntoCwd("cd "+w+" && x", work, "powershell"); got != "cd "+w+" && x" {
		t.Errorf("powershell: %q", got)
	}
}

// A task's Check that cds into its own worktree first gets the decision of
// the rest of the line.
func TestCheckThatCdsIntoItsWorktree(t *testing.T) {
	p := policy(t)
	work := pathutil.Resolved(t.TempDir())
	w := filepath.ToSlash(work)
	for _, tc := range []struct{ command, want string }{
		{"cd " + w + " && go test ./...", Allow},
		{"cd " + w + " && cat a.md", Allow},
		{"cd " + w + " && rm -rf /", Deny},
		{"cd " + w + "/.. && go test ./...", Ask},
	} {
		d := Evaluate(p, LeadRules{}, mode.Full, Action{Kind: "exec", Target: tc.command, Role: "implement", Work: work, Cwd: work, State: work})
		if d.Kind != tc.want {
			t.Errorf("%q: %s (%s, %s), want %s", tc.command, d.Kind, d.Rule, d.Reason, tc.want)
		}
	}
}
