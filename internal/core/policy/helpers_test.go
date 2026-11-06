package policy

import (
	"testing"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// policy loads the default policy from an empty home, so no settings of the
// machine that runs the tests change a decision.
func policy(t testing.TB) config.Policy {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("USERPROFILE", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("APPDATA", dir)
	c, err := configfile.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return c.Policy
}

func check(t *testing.T, p config.Policy, lead LeadRules, m mode.Mode, a Action, want string) {
	t.Helper()
	d := Evaluate(p, lead, m, a)
	if d.Kind != want || d.Reason == "" || d.Rule == "" || d.Mode != m {
		t.Fatalf("%+v in %s: %+v, want %s with reason, rule and mode", a, m, d, want)
	}
}
