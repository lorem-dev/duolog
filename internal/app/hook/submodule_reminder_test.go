package hook

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

func TestSessionStartOmitsEmptySubmoduleLine(t *testing.T) {
	e, _, _ := setup(t, "claude")
	e.RefreshSkills = func(string) string { return "REFRESH_SENTINEL" }
	out := content(t, SessionStart(context.Background(), e), "SessionStart")
	if !strings.Contains(out, "\nREFRESH_SENTINEL\n") || strings.Contains(out, "\n\nREFRESH_SENTINEL") {
		t.Fatalf("empty submodule line in context: %q", out)
	}
}

func TestSessionStartListsDuologSubmodules(t *testing.T) {
	e, _, _ := setup(t, "claude")
	f := submodule.New(t)
	for _, root := range []string{f.Superproject, f.Submodule} {
		if err := os.MkdirAll(filepath.Join(root, ".duolog"), 0700); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(root, ".duolog/config.yaml"), "{}\n")
	}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		write(t, filepath.Join(f.Submodule, name), "INSTRUCTION_SENTINEL")
	}
	e.Root, e.WorkRoot = f.Superproject, f.Superproject
	start := string(SessionStart(context.Background(), e).Stdout)
	if !strings.Contains(start, "Submodules with their own duolog rules: module.") || strings.Contains(start, "INSTRUCTION_SENTINEL") {
		t.Fatal(start)
	}
	e.Environ = append(e.Environ, "DUOLOG_DEPTH=1")
	if out := string(SessionStart(context.Background(), e).Stdout); strings.Contains(out, "Submodules with") {
		t.Fatal(out)
	}
	e.Environ = []string{"CLAUDE_CODE_ENTRYPOINT=cli"}
	e.Root, e.WorkRoot = f.Unrelated, f.Unrelated
	if out := string(SessionStart(context.Background(), e).Stdout); strings.Contains(out, "Submodules with") {
		t.Fatal(out)
	}
	if _, err := os.Stat(filepath.Join(f.Unrelated, ".duolog/sessions.json")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
