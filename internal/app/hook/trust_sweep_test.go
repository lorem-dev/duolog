package hook

import (
	"context"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
)

const sweepLine = "duolog: Codex now trusts the linked worktree /super/sub/.worktrees/x"

func TestSessionStartRunsTrustSweep(t *testing.T) {
	e, _, _ := setup(t, "claude")
	var asked []string
	e.TrustSweep = func(root string, cfg *config.Config, environ []string) string {
		if cfg == nil || len(environ) == 0 {
			t.Fatalf("sweep without config or environment: %v %v", cfg, environ)
		}
		asked = append(asked, root)
		return sweepLine
	}
	c := content(t, SessionStart(context.Background(), e), "SessionStart")
	if !strings.Contains(c, "\n"+sweepLine+"\n") || len(asked) != 1 || asked[0] != e.Root {
		t.Fatalf("context %q, asked %v", c, asked)
	}
	// The sweep runs at session start only.
	if o := Prompt(context.Background(), e); strings.Contains(string(o.Stdout), sweepLine) || len(asked) != 1 {
		t.Fatalf("prompt %q, asked %v", o.Stdout, asked)
	}
}

func TestSessionStartSweepWithoutChangeIsSilent(t *testing.T) {
	e, _, _ := setup(t, "codex")
	e.TrustSweep = func(string, *config.Config, []string) string { return "" }
	if c := content(t, SessionStart(context.Background(), e), "SessionStart"); strings.Contains(c, "trust") {
		t.Fatalf("context %q", c)
	}
	e.TrustSweep = func(string, *config.Config, []string) string { panic("broken sweep") }
	if c := content(t, SessionStart(context.Background(), e), "SessionStart"); strings.Contains(c, "broken") {
		t.Fatalf("context %q", c)
	}
}

func TestWorkerSessionStartSkipsTrustSweep(t *testing.T) {
	e, _, _ := setup(t, "codex")
	e.Environ = append(e.Environ, "DUOLOG_DEPTH=1")
	e.TrustSweep = func(string, *config.Config, []string) string {
		t.Fatal("a worker swept trust")
		return ""
	}
	silent(t, SessionStart(context.Background(), e))
}
