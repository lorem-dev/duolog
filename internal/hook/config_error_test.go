package hook

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// A config that does not load breaks the gate. The lead's session start and
// prompt hooks say so in one line, with the file and the line.
func TestBrokenConfigIsToldToTheLead(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			e, _, _ := setup(t, agent)
			SessionStart(context.Background(), e)
			path := filepath.Join(e.Root, ".duolog", "config.yaml")
			write(t, path, "roles:\n  plan:\n    use:\n      effrot: high\n")
			want := "duolog: config error in " + path + ", line 4: config.roles.plan.use.effrot: unknown key; the gate cannot load its rules\n"
			for event, out := range map[string]Output{
				"SessionStart":     SessionStart(context.Background(), e),
				"UserPromptSubmit": Prompt(context.Background(), e),
			} {
				got := content(t, out, event)
				if strings.Count(got, "config error") != 1 || !strings.Contains(got, want) {
					t.Errorf("%s context %q, want the line %q", event, got, want)
				}
			}
		})
	}
}

// Other hooks and workers stay silent on a broken config, as before.
func TestBrokenConfigLineOnlyForTheLead(t *testing.T) {
	e, _, _ := setup(t, "claude")
	SessionStart(context.Background(), e)
	write(t, filepath.Join(e.Root, ".duolog", "config.yaml"), "bogus: 1\n")
	silent(t, PostTool(context.Background(), e))
	e.Environ = append(e.Environ, "DUOLOG_DEPTH=1")
	silent(t, Prompt(context.Background(), e))
	silent(t, SessionStart(context.Background(), e))
}
