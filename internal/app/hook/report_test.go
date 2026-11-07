package hook

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/policy"
)

// A peer's prompt tells it where to put its final answer, and only the one
// file it may write under .duolog.
func TestPeerPromptNamesTheReport(t *testing.T) {
	pane, _, _, _ := stopSetup(t, session.Entry{})
	got := content(t, Prompt(context.Background(), pane), "UserPromptSubmit")
	for _, want := range []string{policy.ReportPath("peer-abc123"), "result first", "short"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the prompt note lacks %q: %q", want, got)
		}
	}
}

// A lead, a plain worker and a peer ID that could name another path get no
// report note.
func TestReportNoteOnlyForAPlainPeerID(t *testing.T) {
	pane, _, _, _ := stopSetup(t, session.Entry{})
	for name, environ := range map[string][]string{
		"worker":    {"DUOLOG_DEPTH=1"},
		"lead":      {"DUOLOG_PEER=peer-abc123"},
		"dotdot":    {"DUOLOG_DEPTH=1", "DUOLOG_PEER=../x"},
		"separator": {"DUOLOG_DEPTH=1", "DUOLOG_PEER=a/b"},
	} {
		other := pane
		other.Environ = slices.Clone(environ)
		if got := string(Prompt(context.Background(), other).Stdout); strings.Contains(got, "report.md") {
			t.Fatalf("%s got a report note: %q", name, got)
		}
	}
}

// The peer-idle message names the report once the peer has written it, in the
// checkout the peer works in.
func TestPeerIdleNamesTheReport(t *testing.T) {
	pane, r, b, address := stopSetup(t, session.Entry{})
	work := filepath.Join(pane.Root, ".worktrees", "feat-x")
	if _, err := r.Update("peer-abc123", func(e *session.Entry) { e.WorkRoot = work }); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(work, filepath.FromSlash(policy.ReportPath("peer-abc123")))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("PASS\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pane.Input = stopInput(t, "", "done: all good\nmore")
	silent(t, Stop(context.Background(), pane))
	ms := kindMail(t, b, address, "peer-idle")
	want := "peer-abc123 idle in .worktrees/feat-x: done: all good; report " + policy.ReportPath("peer-abc123")
	if len(ms) != 1 || ms[0].S != want {
		t.Fatalf("peer-idle mail %+v, want %q", ms, want)
	}
}

// A report that links out of the checkout is not named.
func TestPeerIdleIgnoresALinkedReport(t *testing.T) {
	pane, _, b, address := stopSetup(t, session.Entry{})
	outside := filepath.Join(t.TempDir(), "x.md")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(pane.Root, filepath.FromSlash(policy.ReportPath("peer-abc123")))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skip("no symlinks:", err)
	}
	pane.Input = stopInput(t, "", "done")
	silent(t, Stop(context.Background(), pane))
	ms := kindMail(t, b, address, "peer-idle")
	if len(ms) != 1 || strings.Contains(ms[0].S, "report") {
		t.Fatalf("peer-idle mail %+v", ms)
	}
}
