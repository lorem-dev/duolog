package gate

import (
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
)

// wantPersonOrDeny checks that a command is denied by the rule given or asks
// only the person, in every mode.
func wantPersonOrDeny(t *testing.T, p config.Policy, root, rule string, commands ...string) {
	t.Helper()
	for _, command := range commands {
		a := Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root}
		for _, m := range []mode.Mode{mode.Ask, mode.Edits, mode.Full} {
			d := Evaluate(p, LeadRules{}, m, a)
			if !PersonOnly(d) && (d.Kind != Deny || !strings.Contains(d.Rule, rule)) {
				t.Errorf("%s: %q: %+v, want the person's ask or a deny by %q", m, command, d, rule)
			}
		}
	}
}

// Inside double quotes the shell removes a backslash only before $, `, ", \
// and a newline, so awk receives "\057etc\057passwd" with its backslashes and
// reads /etc/passwd. The splitter removed every backslash, and the awk check
// saw a plain name.
func TestDoubleQuotesKeepOtherBackslashes(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	// The review's commands.
	wantPersonInFull(t, p, root, `awk "BEGIN{getline x < \"\057etc\057passwd\"; print x}"`)
	wantPersonOrDeny(t, p, root, "read", `awk "BEGIN{getline x < \"\056env\"; print x}"`)
	if got := scriptWords(`awk "a\057b \"c\" \$d \\e \x"`); len(got) != 2 || got[1].text != `a\057b "c" $d \e \x` {
		t.Errorf("scriptWords: %+v", got)
	}
}

// A backslash before a newline, outside single quotes, continues the line:
// the shell removes both, in a plain word and inside double quotes. The
// splitter kept the newline and judged a different name.
func TestBackslashNewlineContinuesTheWord(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	wantPersonInFull(t, p, root,
		// The review's command.
		"cat \"..\\\n/peer/f\"",
		"cat ..\\\n/peer/f",
		"grep x \"..\\\n/peer/f\"",
	)
	wantDeny(t, p, root, "read",
		"cat \".e\\\nnv\"",
		"cat .e\\\nnv",
	)
	// In single quotes the backslash and the newline stay.
	if got, _ := shellWords("cat '..\\\n/x'"); len(got) != 2 || got[1].text != "..\\\n/x" {
		t.Errorf("shellWords: %+v", got)
	}
	if got := scriptWords("cat a \\\n b"); len(got) != 3 || got[1].text != "a" || got[2].text != "b" {
		t.Errorf("scriptWords: %+v", got)
	}
}
