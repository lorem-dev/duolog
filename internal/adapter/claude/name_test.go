package claude

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/run"
)

func writeSessionRecord(t *testing.T, dir string, pid int, body string) {
	t.Helper()
	path := filepath.Join(dir, "sessions", strconv.Itoa(pid)+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestLeadNameReadsClaudesSessionRecord: the lead's display name is the name
// field of <claude home>/sessions/<pid>.json.
func TestLeadNameReadsClaudesSessionRecord(t *testing.T) {
	home := isolateHome(t)
	claudeHome := filepath.Join(home, "claude-config")
	writeSessionRecord(t, claudeHome, 4242, `{"pid":4242,"sessionId":"abc","name":"Fix the parser","kind":"interactive"}`)
	writeSessionRecord(t, claudeHome, 4243, `{"pid":4243,"sessionId":"abd"}`)
	writeSessionRecord(t, claudeHome, 4244, `{"name":`)
	writeSessionRecord(t, claudeHome, 4245, `{"name":42}`)
	environ := os.Environ()
	for _, tc := range []struct {
		pid  int
		want string
	}{
		{4242, "Fix the parser"},
		{4243, ""},
		{4244, ""},
		{4245, ""},
		{9999, ""},
		{0, ""},
		{-1, ""},
	} {
		if got := (Lead{}).Name(environ, tc.pid); got != tc.want {
			t.Errorf("Name(pid %d) = %q, want %q", tc.pid, got, tc.want)
		}
	}
	if got := (Lead{}).Name(nil, 4242); got != "" {
		t.Errorf("Name without a home = %q, want empty", got)
	}
}

// A record that is a link or larger than the cap is not read.
func TestLeadNameReadsOnlyARegularRecordOfBoundedSize(t *testing.T) {
	home := isolateHome(t)
	claudeHome := filepath.Join(home, "claude-config")
	writeSessionRecord(t, claudeHome, 1, `{"name":"Real"}`)
	sessions := filepath.Join(claudeHome, "sessions")
	if err := os.Symlink(filepath.Join(sessions, "1.json"), filepath.Join(sessions, "2.json")); err != nil {
		t.Skip("no symlinks:", err)
	}
	writeSessionRecord(t, claudeHome, 3, `{"name":"`+strings.Repeat("x", 70<<10)+`"}`)
	for pid, want := range map[int]string{1: "Real", 2: "", 3: ""} {
		if got := (Lead{}).Name(os.Environ(), pid); got != want {
			t.Errorf("Name(pid %d) = %q, want %q", pid, got, want)
		}
	}
}

func TestLeadNameFollowsTheDefaultHome(t *testing.T) {
	home := isolateHome(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	writeSessionRecord(t, filepath.Join(home, ".claude"), 77, `{"name":"Docs"}`)
	if got := (Lead{}).Name(os.Environ(), 77); got != "Docs" {
		t.Fatalf("Name = %q", got)
	}
}

// TestWorkerAndPeerStartWithAName: a Claude worker and a Claude peer carry
// --name before the end-of-options marker, as one argument; none given adds no
// flag, and a skill test run is left as it was.
func TestWorkerAndPeerStartWithAName(t *testing.T) {
	p := corelaunch.Process{Prefix: []string{"claude"}, Launch: []string{"-p"}}
	r := route.Route{Target: config.Target{Agent: "claude", Model: "opus"}}
	name := "duolog-worker-implement (lead: fix \"it\" $HOME ;)"
	peerName := "duolog-peer-implement (lead: Docs)"
	for kind, args := range map[string][]string{
		"worker": Worker{}.Argv(run.Spec{Route: r, Process: &p, SessionName: name}),
		"peer":   Pane{}.Argv(r, mode.Ask, p, peerName),
	} {
		i := slices.Index(args, "--name")
		want := name
		if kind == "peer" {
			want = peerName
		}
		if i < 0 || i+1 >= len(args) || args[i+1] != want {
			t.Errorf("%s: %q", kind, args)
		}
		if end := slices.Index(args, "--"); end >= 0 && end < i {
			t.Errorf("%s: --name after the end of options: %q", kind, args)
		}
	}
	for kind, args := range map[string][]string{
		"worker": Worker{}.Argv(run.Spec{Route: r, Process: &p}),
		"peer":   Pane{}.Argv(r, mode.Ask, p, ""),
		"skill":  Worker{}.Argv(run.Spec{Route: r, Process: &p, SkillTest: true, SessionName: name}),
	} {
		if slices.Contains(args, "--name") {
			t.Errorf("%s: %q", kind, args)
		}
	}
}
