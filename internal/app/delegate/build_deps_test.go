package delegate_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/delegate/delegatetest"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/store"
)

// Run builds the worker's process through Deps.Build: every build is the
// worker shape, and the process the login check reads is the one Build gave.
// The account gate builds the admitted process itself, with the boundary's
// Build, so the run's own process is the gate's and is not compared here.
func TestDelegateBuildsThroughDeps(t *testing.T) {
	// No t.Parallel: the test sets the environment and the working folder.
	home := t.TempDir()
	for _, name := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(name, home)
	}
	for _, name := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR", "DUOLOG_DEPTH", "DUOLOG_LEAD"} {
		t.Setenv(name, "")
	}
	root := t.TempDir()
	t.Chdir(root)
	cfg, err := configfile.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := session.Open(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterLead(session.Lead{ID: "lead-123456", Target: config.Target{Agent: "codex", Model: "lead-model"}, Mode: mode.Ask}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	task := filepath.Join(root, "task.md")
	body := "# Task: example\n\n## Goal\nDo work.\n\n## Read\n- a.go\n\n## Write\n- a.go\n\n## Limits\nStay focused.\n\n## Check\ngo test ./...\n"
	if err := os.WriteFile(task, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	fake := delegatetest.New(t)
	deps := fake.Deps()
	real := deps.Build
	var built []corelaunch.Process
	deps.Build = func(in corelaunch.ProcessInput) (corelaunch.Process, error) {
		p, err := real(in)
		p.CacheKey = "from-deps-build"
		built = append(built, p)
		return p, err
	}
	var checked []corelaunch.Process
	deps.NewChecker = func(cacheDir string, p corelaunch.Process) delegate.Checker {
		checked = append(checked, p)
		return delegate.NewChecker(deps, cacheDir, p)
	}
	use := &config.Target{Agent: "claude", Model: "sonnet", Effort: "medium"}
	out, err := delegate.Run(context.Background(), deps, delegate.Request{Config: cfg, StateDir: s.Dir, Role: "implement", TaskPath: task, Thread: "topic",
		Options: delegate.Options{Entry: use, Person: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("RunWorker calls = %d, want 1 (line %q)", len(fake.Calls), out.Line)
	}
	if len(built) == 0 {
		t.Fatal("Deps.Build was not called")
	}
	for _, p := range built {
		if p.Shape != corelaunch.Worker {
			t.Errorf("Build got shape %q, want %q", p.Shape, corelaunch.Worker)
		}
	}
	if len(checked) == 0 || checked[0].CacheKey != "from-deps-build" {
		t.Errorf("the login check did not get the process Deps.Build made: %+v", checked)
	}
}
