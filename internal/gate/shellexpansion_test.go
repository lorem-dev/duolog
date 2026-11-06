package gate

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/mode"
)

// bash's $'...' and $"..." quoting and a brace expansion turn the text the gate
// reads into other words: cat $'..'/peer/f reads ../peer/f, and
// cat {a..c}.env reads a.env, b.env and c.env. The gate does not expand them,
// so such a line needs the person in every mode.
func TestShellExpansionNeedsThePerson(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	wantPersonInFull(t, p, root,
		// The commands of the lead's ruling.
		`cat $'.env'`,
		`cat $'..'/peer/f`,
		`cat {..,.}/peer/f`,
		`cat {a..c}.env`,
		`cat $".."/peer/f`,
		`grep x $'\x2e\x2e/peer/f'`,
		`cat go.mod > {..,.}/peer/f`,
		`echo "$(cat $'.env')"`,
		"echo `cat {.,..}/peer/f`",
		`cat x{'..',.}/peer/f`,
	)
	// A deny that already matches still wins.
	wantDeny(t, p, root, "read", `cat .env {a,b}`)
}

// Inside quotes neither is expanded, and ${...} is a parameter, not a brace
// expansion: these lines stay as they were.
func TestQuotedShellExpansionStaysAsBefore(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	for _, command := range []string{
		`grep '{a,b}' go.mod`,
		`grep "{a..c}" go.mod`,
		`grep "$'x'" go.mod`,
		`grep '$'"'"'x' go.mod`,
		`grep \{a,b} go.mod`,
		`echo ${x,,} ${y/../z}`,
		`find . -name x -exec cat {} \;`,
		`{ cat go.mod, x; }`,
		`echo $$'x'`,
	} {
		a := Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root}
		if shellExpansion(command) {
			t.Errorf("%q: shellExpansion true", command)
		}
		if ExecOutside(a) {
			t.Errorf("%q: ExecOutside true", command)
		}
		if d := Evaluate(p, LeadRules{}, mode.Edits, a); PersonOnly(d) {
			t.Errorf("%q: %+v, want no person-only ask", command, d)
		}
	}
}
