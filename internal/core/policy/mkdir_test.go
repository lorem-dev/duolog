package policy

import (
	"testing"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// A writing role may make a folder in the work root in edits mode, as a task
// that adds a package needs. The write denies, the outside ask, the agent and
// command denies and the read-only roles still hold.
func TestWriterMakesFolders(t *testing.T) {
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(k, home)
	}
	cfg, err := configfile.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	eval := func(command, role string) Decision {
		return Evaluate(cfg.Policy, LeadRules{}, mode.Edits, Action{Kind: "exec", Target: command, Role: role, Work: work})
	}
	for _, c := range []struct{ command, role, want string }{
		{"mkdir internal/foo", "implement", Allow},
		{"mkdir -p internal/foo/bar docs/x", "tests", Allow},
		{"mkdir internal/foo", "review_cross", Deny},
		{"mkdir .git/x", "implement", Deny},
		{"mkdir -p .duolog/x", "implement", Deny},
		{"mkdir -p a .claude/hooks/x", "implement", Deny},
		{"mkdir $(claude)", "implement", Deny},
		{"mkdir a; rm -rf b", "implement", Deny},
		{"mkdir ../x", "implement", Ask},
		{"mkdir /tmp/x", "implement", Ask},
	} {
		if d := eval(c.command, c.role); d.Kind != c.want {
			t.Errorf("%s %q: %+v, want %s", c.role, c.command, d, c.want)
		}
	}
}
