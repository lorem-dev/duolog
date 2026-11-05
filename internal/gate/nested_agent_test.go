package gate

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
)

// A worker that starts claude or codex itself runs outside duolog's routing
// and quota. Under a full lead a policy ask becomes allow, so the default
// policy must deny these execs outright.
func TestWorkersCannotStartAgents(t *testing.T) {
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(k, home)
	}
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"claude -p hi", "claude", "codex exec hi", "/usr/local/bin/claude -p hi", "git status && codex exec x", "claude.exe -p hi"} {
		a := Action{Kind: "exec", Target: command, Role: "implement"}
		if d := Evaluate(cfg.Policy, LeadRules{}, mode.Full, a); d.Kind != Deny {
			t.Errorf("%q: %+v", command, d)
		}
	}
	for _, command := range []string{"claudette build", "codexgen test"} {
		if d := Evaluate(cfg.Policy, LeadRules{}, mode.Full, Action{Kind: "exec", Target: command, Role: "implement"}); d.Kind == Deny {
			t.Errorf("%q denied: %+v", command, d)
		}
	}
}
