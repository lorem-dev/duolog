package gate

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/mode"
)

// Full mode confirms the asks of the project's rules, but a command that
// reaches outside the work root still needs the person, as paths outside the
// tree ask in every mode. Before, the final rule loop turned such an ask into
// an allow in full mode: gawk -E /etc/passwd read the file with no question.
func TestFullModeKeepsThePersonsOutsideAsk(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	for _, command := range []string{
		`gawk -E /etc/passwd`,
		`gawk --exec /etc/passwd`,
		`sed '1r/etc/passwd' go.mod`,
		`cp ../peer/x out`,
		`make -C../peer check`,
		`rg --ignore-file=/etc/x pat`,
	} {
		command = onVolume(root, command)
		a := Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root}
		for _, m := range []mode.Mode{mode.Full, mode.Edits, mode.Ask} {
			if d := Evaluate(p, LeadRules{}, m, a); !PersonOnly(d) {
				t.Errorf("%s: %q: %+v, want the person's ask", m, command, d)
			}
		}
	}
	// Full mode still confirms an ask inside the work root.
	for _, command := range []string{`make x`, `cp a b`, `sed s/a/b/ go.mod`} {
		a := Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root}
		check(t, p, LeadRules{}, mode.Full, a, Allow)
	}
}
