package peer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/peer/peertest"
	"github.com/lorem-dev/duolog/internal/core/config"
)

// The gate judges the command of a peer's prompt from the peer's own worktree,
// wherever the command that settles the question runs: a wildcard rule does
// not allow a file of another worktree, and does allow one of the peer's. The
// path has forward slashes on every host: the gate asks about a line with a
// backslash, which may be an escape, before it looks at any path.
func TestAutoAnswerJudgesACommandFromThePeersRoot(t *testing.T) {
	for _, tc := range []struct {
		name     string
		peerFile bool
		keys     string
	}{
		{"a file in the caller's worktree is outside the peer", false, ""},
		{"a file in the peer's worktree is inside it", true, "enter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, one, two := twoWorktrees(t)
			r.cfg.Policy.Rules = append(r.cfg.Policy.Rules, config.Rule{Action: "exec", Match: "task-check *", Decision: "allow", Roles: []string{"implement"}})
			t.Chdir(one)
			opened := r.open("implement")

			t.Chdir(two)
			dir := two
			if tc.peerFile {
				dir = one
			}
			r.panes(withScreens(commandPrompt("task-check "+filepath.ToSlash(filepath.Join(dir, "x")), false), codexIdle("")))
			if _, err := Status(r.ctx, opened.ID); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(r.keys(), ","); got != tc.keys {
				t.Fatalf("keys %q, want %q", got, tc.keys)
			}
		})
	}
}

// An assignment can point a command at another repository, so auto-answer
// judges the line as the pane shows it: these stay with the person.
func TestAutoAnswerJudgesTheAssignmentsOfACommand(t *testing.T) {
	for _, form := range []string{
		`GIT_DIR=%s/.git git log`,
		`GIT_WORK_TREE=%s git status`,
		`GIT_CONFIG_GLOBAL=%s/x git log`,
		`PATH=%s/bin git log`,
		`GIT_DIR='%s/.git' git log`,
	} {
		t.Run(form, func(t *testing.T) {
			r, one, two := twoWorktrees(t)
			t.Chdir(one)
			opened := r.open("implement")
			r.panes(withScreens(commandPrompt(strings.ReplaceAll(form, "%s", two), false), codexIdle("")))
			if _, err := Status(r.ctx, opened.ID); err != nil {
				t.Fatal(err)
			}
			if got := r.keys(); len(got) != 0 {
				t.Fatalf("answered with keys %v", got)
			}
		})
	}
}

// Git variables of the caller do not choose the tree that a peer's task is
// compared with: the snapshot is of the folder it is given.
func TestSnapshotIgnoresGitVariablesOfTheCaller(t *testing.T) {
	r, one, two := twoWorktrees(t)
	if err := os.WriteFile(filepath.Join(two, "only-two.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(r.root, ".git", "worktrees", "one"))
	t.Setenv("GIT_WORK_TREE", one)
	got, err := delegate.Snapshot(two)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["only-two.txt"]; !ok {
		t.Fatalf("snapshot of the second worktree: %v", got)
	}
}

// twoWorktrees makes the rig's root a repository with two linked worktrees,
// and gives each open its own pane. The roles it opens run on one agent, so
// that only the role and the work root can tell their peers apart.
func twoWorktrees(t *testing.T, roles ...string) (*rig, string, string) {
	t.Helper()
	script := baseScript()
	script["open-session"] = peertest.Reply{Session: "session:peers", Block: "block:0"}
	script["open-tab"] = peertest.Reply{Sequence: []peertest.Reply{{Block: "block:1"}, {Block: "block:2"}, {Block: "block:3"}, {Block: "block:4"}}}
	r := newRig(t, script)
	r.cfg.Limits.MaxLiveSessions = 8
	for _, role := range roles {
		r.cfg.Roles[role] = r.cfg.Roles["implement"]
	}
	initRepo(t, r.root)
	var trees []string
	for _, name := range []string{"one", "two"} {
		tree := filepath.Join(r.root, ".worktrees", name)
		gitIn(t, r.root, "worktree", "add", "-q", "-b", name, tree)
		trees = append(trees, tree)
	}
	return r, trees[0], trees[1]
}

// taskIn writes a valid task file in dir and returns its path.
func taskIn(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "task.md")
	body := "# Task: example\n\n## Goal\nDo work.\n\n## Read\n- a.go\n\n## Write\n- a.go\n\n## Limits\nStay focused.\n\n## Check\ngo test ./...\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A peer belongs to its role and its work root: an open that differs in
// either starts a new peer, and only the same role in the same worktree
// returns the live one.
func TestOpenReusesOnlyTheSameRoleInTheSameWorktree(t *testing.T) {
	for _, tc := range []struct {
		name, role string
		secondTree bool
	}{
		{"another role in another worktree", "implement", true},
		{"the same role in another worktree", "debug_hard", true},
		{"another role in the same worktree", "implement", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, one, two := twoWorktrees(t, "debug_hard")
			t.Chdir(one)
			first := r.open("debug_hard")

			want := one
			if tc.secondTree {
				want = two
			}
			t.Chdir(want)
			other := r.open(tc.role)
			if other.ID == first.ID {
				t.Fatalf("got the live peer %s: %+v", first.ID, r.entry(first.ID))
			}
			if got := r.entry(other.ID); got.Role != tc.role || !sameWorkRoot(got.WorkRoot, want) {
				t.Fatalf("%+v", got)
			}

			t.Chdir(one)
			if again := r.open("debug_hard"); again.ID != first.ID {
				t.Fatalf("the same role here got %s, want %s", again.ID, first.ID)
			}
			if got := r.entry(first.ID); got.Role != "debug_hard" || !sameWorkRoot(got.WorkRoot, one) {
				t.Fatalf("%+v", got)
			}
		})
	}
}

// Git variables of the caller, as a Git hook exports them, do not say which
// worktree the open came from: the folder does.
func TestOpenIgnoresGitVariablesOfTheCaller(t *testing.T) {
	r, one, two := twoWorktrees(t)

	t.Chdir(one)
	first := r.open("implement")

	t.Chdir(two)
	t.Setenv("GIT_DIR", filepath.Join(r.root, ".git", "worktrees", "one"))
	t.Setenv("GIT_WORK_TREE", one)
	second := r.open("implement")
	if second.ID == first.ID {
		t.Fatalf("an open in the second worktree got the first worktree's peer %s", first.ID)
	}
	if got := r.entry(second.ID); !sameWorkRoot(got.WorkRoot, two) {
		t.Fatalf("recorded work root %s, want %s", got.WorkRoot, two)
	}
}

// A task outside the peer's own work root never reaches the pane, whether it
// is named from the caller's worktree or by its absolute path.
func TestSendRefusesATaskOutsideThePeersWorkRoot(t *testing.T) {
	r, one, two := twoWorktrees(t)
	t.Chdir(one)
	opened := r.open("implement")
	taskIn(t, one)

	t.Chdir(two)
	other := taskIn(t, two)
	for _, ref := range []string{"task.md", other} {
		err := Send(r.ctx, opened.ID, ref)
		if err == nil || !strings.Contains(err.Error(), "work root") || !strings.Contains(err.Error(), opened.ID) {
			t.Fatalf("send %s: %v", ref, err)
		}
	}
	if sent := r.named("send"); len(sent) != 0 {
		t.Fatalf("typed into the pane: %v", sent)
	}
	if got := r.entry(opened.ID); got.Task != "" {
		t.Fatalf("recorded task %s", got.Task)
	}
}

// The same refusal holds when the caller is in the peer's own worktree and
// names a file in another one.
func TestSendRefusesAnotherWorktreesTaskFromThePeersOwn(t *testing.T) {
	r, one, two := twoWorktrees(t)
	t.Chdir(one)
	opened := r.open("implement")
	other := taskIn(t, two)

	err := Send(r.ctx, opened.ID, other)
	if err == nil || !strings.Contains(err.Error(), "work root") || !strings.Contains(err.Error(), opened.ID) {
		t.Fatalf("%v", err)
	}
	if sent := r.named("send"); len(sent) != 0 {
		t.Fatalf("typed into the pane: %v", sent)
	}
}

// A task in the peer's worktree is the peer's, wherever the lead stands. The
// pane is told the path from its own folder.
func TestSendAcceptsATaskInThePeersWorkRootFromElsewhere(t *testing.T) {
	r, one, two := twoWorktrees(t)
	t.Chdir(one)
	opened := r.open("implement")
	path := taskIn(t, one)

	t.Chdir(two)
	script := baseScript()
	script["capture"] = screens(sendOK("task.md")...)
	r.panes(script)
	if err := Send(r.ctx, opened.ID, path); err != nil {
		t.Fatal(err)
	}
	sent := r.named("send")
	if len(sent) == 0 || !strings.Contains(strings.Join(sent[0], " "), "Read task.md and do it") {
		t.Fatalf("%v", sent)
	}
	if got := r.entry(opened.ID); !sameWorkRoot(filepath.Dir(got.Task), one) {
		t.Fatalf("recorded task %s", got.Task)
	}
}
