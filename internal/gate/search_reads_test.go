package gate

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
)

// searchCase evaluates a command that runs in the work root root, with ROOT in
// the command standing for root.
func searchCase(t *testing.T, p config.Policy, root, command string) (Action, Decision) {
	t.Helper()
	command = strings.ReplaceAll(command, "ROOT", filepath.ToSlash(root))
	a := Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root}
	return a, Evaluate(p, LeadRules{}, mode.Edits, a)
}

// checkNotPersonOnly holds commands that read only inside the work root: they
// may ask the lead, but never only the person.
func checkNotPersonOnly(t *testing.T, commands []string) {
	t.Helper()
	p := policy(t)
	root := t.TempDir()
	for _, command := range commands {
		a, d := searchCase(t, p, root, command)
		if PersonOnly(d) || ExecOutside(a) {
			t.Errorf("%q: %+v, outside=%v; want no person-only ask", command, d, ExecOutside(a))
		}
	}
}

// checkStillOutside holds commands that name a file outside the work root, or
// write one: each stays person-only, or is denied.
func checkStillOutside(t *testing.T, commands []string) {
	t.Helper()
	p := policy(t)
	root := t.TempDir()
	for _, command := range commands {
		command = strings.ReplaceAll(command, "/elsewhere", absoluteTestPath(root, "/elsewhere"))
		_, d := searchCase(t, p, root, command)
		if d.Kind != Deny && !PersonOnly(d) {
			t.Errorf("%q: %+v; want a person-only ask or a deny", command, d)
		}
	}
}

// The line ran with a here-document whose lines the log joined. The separate
// "> /dev/null" made cat seem to read /dev/null, a path outside the work root.
func TestReadOfStreamDeviceIsNotOutside(t *testing.T) {
	checkNotPersonOnly(t, []string{
		`cd ROOT/.worktrees/exp-gate-c/ && git stash list | head -2; cat > /dev/null <<'EOF' EOF git log --oneline develop -1 2>&1 | head -2`,
		`cat /dev/null`,
		`cat > /dev/null; ls`,
		`head -c 4 /dev/stdin`,
		`wc -l /dev/fd/3`,
	})
	checkStillOutside(t, []string{
		`cat /elsewhere/dev/null`,
		`cat /dev/zero`,
		`cat /dev/tty`,
		`cat > /dev/null; cat ../peer/f`,
		`cat /dev/null ../peer/f`,
	})
}
