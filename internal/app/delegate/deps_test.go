package delegate_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/delegate/delegatetest"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
)

// Run reaches the worker, the model lookup, the gate and the rest only through
// Deps, and never the user's home.
func TestRunUsesDeps(t *testing.T) {
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
	// implement routes to Codex by default, whose hooks need the user's home;
	// the request pins Claude, as --use does.
	use := &config.Target{Agent: "claude", Model: "sonnet", Effort: "medium"}
	out, err := delegate.Run(context.Background(), fake.Deps(), delegate.Request{Config: cfg, StateDir: s.Dir, Role: "implement", TaskPath: task, Thread: "topic",
		Options: delegate.Options{Entry: use, Person: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("RunWorker calls = %d, want 1 (line %q)", len(fake.Calls), out.Line)
	}
	if got := fake.Calls[0].Route.Agent; got != "claude" {
		t.Fatalf("worker agent = %q, want claude", got)
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Fatalf("home folder touched: %v, %v", entries, err)
	}
}
