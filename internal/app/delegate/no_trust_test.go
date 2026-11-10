package delegate_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/delegate/delegatetest"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/store"
)

// An agent with no Trust fails closed: its hooks read as untrusted, so a write
// role (implement) is refused with the text an untrusted Codex gives, and a
// read-only role (debug) runs.
func TestNoTrustRefusesWriteRoles(t *testing.T) {
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
	// Codex, whose read-only sandbox holds the role by itself: a Claude
	// read-only role is held by the gate alone, and with no Trust that gate
	// cannot be shown to run, so it is refused too.
	use := &config.Target{Agent: "codex", Model: "sol", Effort: "medium"}
	run := func(role string) (*delegatetest.Fake, delegate.Outcome, error) {
		fake := delegatetest.New(t)
		deps := fake.Deps()
		for name, a := range deps.Agents {
			a.Trust = nil
			deps.Agents[name] = a
		}
		out, err := delegate.Run(context.Background(), deps, delegate.Request{Config: cfg, StateDir: s.Dir, Role: role, TaskPath: task, Thread: "topic-" + role,
			Options: delegate.Options{Entry: use, Person: true}})
		return fake, out, err
	}

	fake, out, err := run("implement")
	if err == nil || !strings.Contains(err.Error(), "Codex hooks untrusted for write role implement in ") {
		t.Fatalf("implement with no Trust: err %v, line %q; want the untrusted-hooks refusal", err, out.Line)
	}
	if len(fake.Calls) != 0 {
		t.Fatalf("a write role started with no Trust: %d worker calls", len(fake.Calls))
	}

	// debug is a read-only role.
	fake, out, err = run("debug")
	if err != nil {
		t.Fatalf("debug with no Trust: %v (line %q)", err, out.Line)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("debug with no Trust: %d worker calls, want 1 (line %q)", len(fake.Calls), out.Line)
	}
}
