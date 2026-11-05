package doctor

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/workerenv"
)

// Doctor warns every time while a dropped key is set or listed and paid runs
// do not let it through, with a paid_runs example that keeps the person's
// paid env, and never prints the key's value.
func TestDoctorRepeatsMigrationWhileKeyDropped(t *testing.T) {
	const secret = "sk-test-value"
	cfg, opts, _ := setup(t)
	t.Setenv("ANTHROPIC_API_KEY", secret)
	t.Setenv("OPENAI_API_KEY", "")
	if err := os.Unsetenv("OPENAI_API_KEY"); err != nil {
		t.Fatal(err)
	}
	cfg.PaidRuns.Claude.Env = []string{"CLAUDE_PAID_EXTRA"}
	claude := workerenv.MigrationLine("claude", "ANTHROPIC_API_KEY")
	codex := workerenv.MigrationLine("codex", "OPENAI_API_KEY")
	// An explicit list replaces the default, so the example keeps the paid env.
	example := "Example config: paid_runs: {claude: {allow: true, daily_cap_usd: 5, env: [CLAUDE_PAID_EXTRA, ANTHROPIC_API_KEY]}}"
	warned := func(got []Finding, line string) bool {
		for _, f := range got {
			if f.Level == "warning" && strings.Contains(f.Message, line) && strings.Contains(f.Message, example) {
				return true
			}
		}
		return false
	}
	for range 2 {
		got := Run(context.Background(), cfg, opts)
		if !warned(got, claude) {
			t.Fatalf("no migration warning with %q: %+v", example, got)
		}
		for _, f := range got {
			if strings.Contains(f.Message, secret) || strings.Contains(f.Message, codex) || strings.Contains(f.Message, "env_passthrough") {
				t.Fatalf("%+v", f)
			}
		}
	}

	// Listed in env_passthrough without paid runs: the key still does not
	// reach workers, so the warning stays, set or not.
	base := slices.Clone(cfg.EnvPassthrough)
	cfg.EnvPassthrough = append(cfg.EnvPassthrough, "ANTHROPIC_API_KEY", "OPENAI_API_KEY")
	if got := Run(context.Background(), cfg, opts); !warned(got, claude) || !slices.ContainsFunc(got, func(f Finding) bool {
		return strings.Contains(f.Message, codex) && strings.Contains(f.Message, "paid_runs: {codex: {allow: true, daily_cap_usd: 5, env: [OPENAI_API_KEY]}}")
	}) {
		t.Fatalf("listed keys not warned: %+v", got)
	}

	// Allowed with the key in the paid env: the key reaches paid runs, so no
	// warning for that agent.
	cfg.EnvPassthrough = base
	cfg.PaidRuns.Claude.Allow, cfg.PaidRuns.Claude.DailyCapUSD = true, 5
	cfg.PaidRuns.Claude.Env = []string{"ANTHROPIC_API_KEY"}
	for _, f := range Run(context.Background(), cfg, opts) {
		if strings.Contains(f.Message, "no longer get") {
			t.Fatalf("allowed key still warned: %+v", f)
		}
	}

	// Neither set nor listed: no warning.
	cfg.PaidRuns.Claude.Allow = false
	if err := os.Unsetenv("ANTHROPIC_API_KEY"); err != nil {
		t.Fatal(err)
	}
	for _, f := range Run(context.Background(), cfg, opts) {
		if strings.Contains(f.Message, "no longer get") {
			t.Fatalf("%+v", f)
		}
	}
}
