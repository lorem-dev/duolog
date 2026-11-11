package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

// Git processes origin/develop's gate started for one tool call in a project
// without submodules, the lead in its main checkout and the worker in a linked
// worktree of it. The lead: the state root of the folder and two work root
// lookups (the guard and the observer). The worker: the state root, the work
// root, and the state root of its work root for its inherited settings.
const (
	developLeadGitCalls   = 3
	developWorkerGitCalls = 3
)

// A project without submodules pays nothing in Git for the lead home or the
// worker's place against its lead. Each call runs in a project of its own, so
// no lead home cached by an earlier call of this test process hides a cost.
func TestGateGitCallsWithoutSubmodules(t *testing.T) {
	isolatedRouteConfig(t)
	t.Setenv("DUOLOG_LEAD", "lead-123456")
	t.Setenv("DUOLOG_ROLE", "implement")
	t.Setenv("DUOLOG_MODE", "")
	leadMain, _ := gitCountProject(t)
	_, workerWork := gitCountProject(t)
	calls := countGitCalls(t)

	t.Setenv("DUOLOG_DEPTH", "")
	t.Chdir(leadMain)
	runGate(t, "ls")
	lead := calls()
	t.Logf("lead gate Git calls: %q", lead)
	if len(lead) > developLeadGitCalls {
		t.Fatalf("lead gate ran %d Git processes, develop ran %d: %q", len(lead), developLeadGitCalls, lead)
	}

	t.Setenv("DUOLOG_DEPTH", "1")
	t.Chdir(workerWork)
	if out := runGate(t, "ls"); out != "" {
		t.Fatal(out)
	}
	worker := calls()
	t.Logf("worker gate Git calls: %q", worker)
	if len(worker) > developWorkerGitCalls {
		t.Fatalf("worker gate ran %d Git processes, develop ran %d: %q", len(worker), developWorkerGitCalls, worker)
	}
}

// gitCountProject is a repository with duolog state, a lead recorded in its
// main checkout and a linked worktree under .worktrees.
func gitCountProject(t *testing.T) (main, work string) {
	t.Helper()
	main = filepath.Join(t.TempDir(), "p")
	submodule.Init(t, main)
	work = filepath.Join(main, ".worktrees", "x")
	submodule.Git(t, main, "worktree", "add", "-q", "--detach", work)
	reg, err := session.Open(filepath.Join(main, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(main, ".duolog", "config.yaml"), []byte("policy:\n  ask_timeout_seconds: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterLead(session.Lead{ID: "lead-123456", Target: config.Target{Agent: "codex"}, Root: main, Mode: mode.Full}); err != nil {
		t.Fatal(err)
	}
	return main, work
}

// countGitCalls puts a git on PATH that records each call and runs the real
// one, and returns a function that reads and clears the calls made so far.
func countGitCalls(t *testing.T) func() []string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the counting git is a shell script")
	}
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	bin, log := filepath.Join(dir, "bin"), filepath.Join(dir, "calls")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$GITCOUNT_LOG\"\nexec \"$GITCOUNT_REAL\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITCOUNT_LOG", log)
	t.Setenv("GITCOUNT_REAL", real)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		data, err := os.ReadFile(log)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(log); err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSpace(string(data)), "\n")
	}
}
