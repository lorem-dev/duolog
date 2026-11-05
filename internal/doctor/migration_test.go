package doctor

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/workerenv"
)

// Doctor warns every time while a dropped key is set and not listed, with an
// env_passthrough example that keeps the person's list, and never prints the
// key's value or the paid_runs key, which the config does not accept yet.
func TestDoctorRepeatsMigrationWhileKeyDropped(t *testing.T) {
	const secret = "sk-test-value"
	cfg, opts, _ := setup(t)
	t.Setenv("ANTHROPIC_API_KEY", secret)
	t.Setenv("OPENAI_API_KEY", "")
	if err := os.Unsetenv("OPENAI_API_KEY"); err != nil {
		t.Fatal(err)
	}
	claude := workerenv.MigrationLine("claude", "ANTHROPIC_API_KEY", cfg.EnvPassthrough)
	codex := workerenv.MigrationLine("codex", "OPENAI_API_KEY", cfg.EnvPassthrough)
	// An explicit list replaces the defaults, so the example is the whole
	// list: the defaults in force plus the key.
	example := "env_passthrough: [" + strings.Join(append(slices.Clone(cfg.EnvPassthrough), "ANTHROPIC_API_KEY"), ", ") + "]"
	if !slices.Contains(cfg.EnvPassthrough, "HOME") {
		t.Fatalf("test config lacks the default names: %v", cfg.EnvPassthrough)
	}
	warned := func(got []Finding, line string) bool {
		for _, f := range got {
			if f.Level == "warning" && f.Message == line && strings.HasSuffix(f.Message, example) {
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
			if strings.Contains(f.Message, secret) || strings.Contains(f.Message, codex) || strings.Contains(f.Message, "paid_runs") {
				t.Fatalf("%+v", f)
			}
		}
	}

	// Listed in env_passthrough: the key passes to workers, so no warning,
	// set or not.
	cfg.EnvPassthrough = append(cfg.EnvPassthrough, "ANTHROPIC_API_KEY", "OPENAI_API_KEY")
	for _, f := range Run(context.Background(), cfg, opts) {
		if strings.Contains(f.Message, "no longer get") {
			t.Fatalf("listed key still warned: %+v", f)
		}
	}

	// Neither set nor listed: no warning.
	cfg.EnvPassthrough = cfg.EnvPassthrough[:len(cfg.EnvPassthrough)-2]
	if err := os.Unsetenv("ANTHROPIC_API_KEY"); err != nil {
		t.Fatal(err)
	}
	for _, f := range Run(context.Background(), cfg, opts) {
		if strings.Contains(f.Message, "no longer get") {
			t.Fatalf("%+v", f)
		}
	}
}
