package policy

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/core/mode"
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
	// A policy rule with a wildcard, such as make {check,...}*, allows a
	// command, but not its paths outside the work root.
	for _, command := range []string{
		`make check -C ../peer`,
		`gofmt -w ../peer/main.go`,
		`go build -o /tmp/x .`,
		`black ../peer`,
		`node_modules/.bin/prettier --write ../peer/src`,
	} {
		command = onVolume(root, command)
		a := Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root}
		for _, m := range []mode.Mode{mode.Full, mode.Edits, mode.Ask} {
			if d := Evaluate(p, LeadRules{}, m, a); !PersonOnly(d) {
				t.Errorf("%s: %q: %+v, want the person's ask", m, command, d)
			}
		}
	}
	for _, command := range []string{`make check`, `gofmt -w main.go`, `go build -o out .`} {
		a := Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root}
		check(t, p, LeadRules{}, mode.Edits, a, Allow)
	}
	// A sed or awk script that starts with /, and the sets of tr, are not
	// paths, so full mode confirms them as develop did. The parsers judge
	// what a script names.
	for _, command := range []string{
		`sed -i '/^$/d' notes.txt`,
		`sed '/TODO/d' x`,
		`awk '/re/' x`,
		`tr / _ < x`,
		`awk '{n++} /x/ {print}' go.mod`,
		`awk 'BEGIN{x = ++n / 2}'`,
	} {
		a := Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root}
		if ExecOutside(a) {
			t.Errorf("%q: ExecOutside true", command)
		}
		if d := Evaluate(p, LeadRules{}, mode.Full, a); PersonOnly(d) || d.Kind == Deny {
			t.Errorf("full: %q: %+v, want no person's ask", command, d)
		}
	}
	// An input redirect makes a line uncertain, so tr / _ < x stays the
	// lead's ask in every mode, as on develop; without it full mode confirms.
	for _, command := range []string{`sed -i '/^$/d' notes.txt`, `sed '/TODO/d' x`, `awk '/re/' x`, `tr / _`} {
		a := Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root}
		check(t, p, LeadRules{}, mode.Full, a, Allow)
	}
	// The words of sed, awk and tr are left to their parsers only when the
	// parsers read the same program: quotes and a backslash can make the
	// split words name sed while the shell runs cat.
	wantPerson(t, p, root,
		`FOO='1 sed' cat /etc/passwd`,
		`FOO=1\ sed cp README.md ../peer/x`,
		`FOO="x tr" cp README.md /tmp/out`,
		`FOO='1 awk' cat /etc/passwd`,
		`env 'FOO=1 sed' cat /etc/passwd`,
	)
	// on_unmatched: allow is a wildcard over every program, and it does not
	// allow a path outside the work root either.
	open := policy(t)
	open.OnUnmatched = Allow
	wantPersonInFull(t, open, root, `cp README.md ../peer/x`, `tar czf home.tgz ~`, `rsync -a src/ /tmp/out`)
	for _, m := range []mode.Mode{mode.Edits, mode.Full} {
		check(t, open, LeadRules{}, m, Action{Kind: "exec", Target: `cp README.md out`, Role: "implement", Work: root, Cwd: root}, Allow)
	}
	// Full mode still confirms an ask inside the work root.
	for _, command := range []string{`make x`, `cp a b`, `sed s/a/b/ go.mod`} {
		a := Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root}
		check(t, p, LeadRules{}, mode.Full, a, Allow)
	}
}
