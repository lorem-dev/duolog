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

// A quoted argument is one word, and a < or > inside quotes redirects
// nothing. Splitting it at its spaces made pieces such as \|^A, T:{{join and
// // look like paths outside the work root. The lines come from real worker
// commands that asked only the person.
func TestQuotedArgumentIsOneWord(t *testing.T) {
	checkNotPersonOnly(t, []string{
		`cat config/schemagen/schema.go | head -60; grep -n "func \|os\.\|PkgPath" config/schema.go | head; grep -n "func " usage/usage.go task/task.go | head -30`,
		`cd ROOT && git worktree add -q --detach .worktrees/rebase-base develop`,
		`cd ROOT/.worktrees/arch-agents && grep -n "^func \|^var \|^	[a-zA-Z]* *= " internal/delegate/delegate_test.go | head -80`,
		`cd ROOT/.worktrees/arch-agents && grep -n "^func \|512\|maxLine\|lineLimit" internal/delegate/delegate.go | head -80`,
		`cd ROOT/.worktrees/b3-budget/.superpowers/tasks && ls -la . tl 2>/dev/null; wc -l plan-ab.md report-b1.md report-stop1.md; grep -n '^## \|^### ' plan-ab.md | head -80`,
		`cd ROOT/.worktrees/probe-develop && go test -count=1 -v -run 'TestTmpProbe' ./internal/gate/ 2>&1 | head -20`,
		`git add -A internal layers_test.go docs_test.go schema_test.go .golangci.yml && git status --short | grep -v "^R \|^A \|^M \|^RM" ; echo staged`,
		`git diff --stat && git diff internal/adapter/config/compact_test.go | grep -n '^[-+ ]*\(<<<<<<<\|=======\|>>>>>>>\)'`,
		`git diff develop -- internal/gate/gate.go | grep -n "uncertain\|'<'\|\"<\"" | head -20`,
		`git grep -l "\"github.com/lorem-dev/duolog/internal/config\"" -- "*.go"`,
		`git grep -ln "env_passthrough" -- . ':!*_test.go' | head -20; git grep -n "^type Lead \|^type Workers " -- '*.go'`,
		`git grep -n -F '"..' -- internal/app/delegate internal/app/profile`,
		`git grep -n 'testFile), ".."' internal/app`,
		`git grep -nE '"\.\.", "\.\."' -- 'internal/app/**/*_test.go'`,
		`go list -f '{{.ImportPath}} T:{{join .TestGoFiles ","}} X:{{join .XTestGoFiles ","}} I:{{join .Imports ","}}' ./internal/gate ./internal/paidruns ./internal/paidruns/paidrunstest ./cmd/duolog`,
		`go list -f '{{.ImportPath}} T:{{join .TestGoFiles ","}} X:{{join .XTestGoFiles ","}}' ./internal/gate ./internal/paidruns ./internal/paidruns/paidrunstest`,
		`go test -count=1 -run TestBuildGolden ./internal/workerenv && git status --short | grep -v '^R \|^RM\|^ M\|^M ' ; git status --short -- '*golden*' testdata`,
		`go test -count=1 ./internal/... 2>&1 | grep -v -E '^(ok |\?)' | head -40`,
		`go test ./internal/gate/ -run TestZZHolesProbe -v 2>&1 | grep -v "^=== RUN" | sed 's/^ *zz_holes_probe_test.go:[0-9]*: //'`,
		`go version && grep -n "^go \|^module" go.mod && sed -n 1,80p Makefile`,
		`gofmt -l . 2>&1 | head; grep -n "^## \|^### " docs/architecture.md | head -50`,
		`grep -ln "func (r \*Registry) ResolveLead" internal/session/lead.go internal/session/resolve.go internal/session/session.go internal/session/leads.go`,
		`grep -n -A25 'func (h \*harness) gateOnPath' internal/delegate/*_test.go`,
		`grep -n -e 'func (t \*fileTargets) union' -A3 internal/gate/filetargets.go; grep -n -e "^	\"" internal/gate/filetargets_test.go | head`,
		`grep -n -e "func (r \*Registry) ObserveLead" -e "func (r \*Registry) EffectiveMode" -e "func (r \*Registry) RegisterLead" -e "func (r \*Registry) ResolveLead" internal/session/session.go`,
		// The quote-stripped parse reads ../peer here, which is only part of the pattern.
		`grep -n "x \.\./peer" f`,
		`sed -n 's/a b/c ../p' f`,
	})
	checkStillOutside(t, []string{
		`grep -n "a b" ../peer/f`,
		`grep -n "^func \|x" ../peer/f | head`,
		`grep "a b" f > ../out`,
		`grep "a b" f >../out`,
		`grep "a b" f 2>>../out`,
		`grep "a b" f>../out`,
		`grep "a b" f >"../out"`,
		`grep "a b" f > /dev/null>../out`,
		`grep "a b" < ../peer/f`,
		`cat "../peer/f"`,
		`cat ".."/peer/f`,
		`cat \.\./peer/f`,
		`cat "a b" '/elsewhere/f'`,
		`grep "$(cat ../x)" f`,
		"grep \"`cat ../x`\" f",
		`grep x <(cat ../x)`,
		`sh -c 'grep x ../peer/f'`,
		`echo 'cat ../x' | sh`,
		`sed 's/a/b/w ../out' f`,
		`sed 's/a/b/e cat ../x' f`,
		`awk '{print > "../out"}' f`,
		`awk 'BEGIN{system("cat ../x")}'`,
		`git -c alias.x='!cat ../x' grep y`,
		`go list -toolexec 'sh ../x' ./...`,
		`git grep -O'vim ../x' y`,
		`git grep --open-files-in-pager='vim ../x' y`,
		`git grep -nO'vim ../x' y`,
		`sed 's/a/b/;w ../out' f`,
		`awk '{print | "cat ../x"}' f`,
		`awk -f ../prog f`,
		`sort -o ../out "a b"`,
	})
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
