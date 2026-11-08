package policy

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
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

// A search pattern is no path: the first operand of grep, egrep, fgrep, rg
// and git grep, each -e, --regexp and rg -r value, the -G and -S of git log
// and diff, the sets of tr and a plain awk program. A pattern such as \.Dir,
// .. or //x read as a path outside the work root, and asked only the person.
func TestSearchPatternIsNoPath(t *testing.T) {
	checkNotPersonOnly(t, []string{
		`awk '/^## Development/{f=1} f{print NR": "$0} /^## [0-9]/{if(f)exit}' CHANGES.md | head -40`,
		`awk '/^## Development/{on=1;next} /^## /{on=0} on && /^- /{n++} END{print n}' CHANGES.md`,
		`git diff HEAD -U0 -G '//.*configfile' --name-only`,
		`git grep -n "\.Dir = " -- internal/delegate internal/peer internal/workerenv ':!*_test.go'`,
		`git grep -n "\.Task = \|\.TaskGeneration = \|Task, .*TaskGeneration\|e\.Task, \|TaskGeneration:" -- 'internal/*.go' 'cmd/*.go' ':!*_test.go'`,
		`go test -list . ./internal/gate | sort | grep -v '^ok' | tr '\n' ' '`,
		`GOOS=darwin make lint-go 2>&1 | grep -e "\.go:" | head`,
		`grep -c -E '\bLoad\(' internal/config/*_test.go; head -3 internal/config/*_test.go | grep -E 'package|==>'`,
		`grep -n -e '\[!' -e 'symlink' docs/permissions.md`,
		`grep -n -E '\bconfig\.[A-Z]' cmd/duolog/calibrate.go internal/peer/peer.go`,
		`grep -n -e "P " -e "\.P\b" -e "person" -e "Person" internal/gate/ask.go`,
		`grep '\.\.' f`,
		`grep .. f`,
		`grep -n -- ../x f`,
		`grep -e ../x f`,
		`grep -ne../x f`,
		`grep --regexp=../x f`,
		`grep --regexp ../x f`,
		`grep -A 3 /x f`,
		`grep -m1 -i /x f`,
		`egrep '/x|\.\./y' f`,
		`fgrep .. f`,
		`rg /x src`,
		`rg --regexp=../x .`,
		`rg -e /x -e ../y src`,
		`rg -r '\1' '(a)' src`,
		`rg --replace=../x a src`,
		`rg -n -g '*.go' '\.\.' src`,
		`git grep .. -- internal`,
		`git grep -e /x -e ../y`,
		`git log -S '/x' --oneline`,
		`git log --oneline -G ../x -- internal`,
		`git show -S '\.Dir' HEAD`,
		`tr '\n' ' ' < f`,
		`tr -d '\\' < f | head`,
		`awk -F: '/^\.\./{print $1}' f`,
	})
	checkStillOutside(t, []string{
		`grep x ../peer/f`,
		`grep '\.\.' ../peer/f`,
		`grep -e x ../peer/f`,
		`grep ../peer/f -e x`,
		`grep ../peer/f --regexp=x`,
		`grep -f ../x f`,
		`grep -f../x f`,
		`grep -nf../x f`,
		`grep --file=/elsewhere/x f`,
		`grep --file ../x f`,
		`grep -e x -f ../x f`,
		`grep --exclude-from ../x pat f`,
		`grep --exclude-from=../x pat f`,
		`grep pat --fil ../x`,
		`grep ../secret --regex x`,
		`grep -C ../x pat ../y`,
		`grep -x -- x ../peer/f`,
		`egrep -e x ../peer/f`,
		`fgrep x -- ../peer/f`,
		`rg -f ../x .`,
		`rg --file=../x .`,
		`rg x ../peer`,
		`rg --files ../peer`,
		`rg --pre ../evil x f`,
		`rg -e x ../peer`,
		`git grep x -- ../peer`,
		`git grep -f ../x`,
		`git grep -f../x y`,
		`git grep --no-index x ../peer/f`,
		`git grep -e x ../peer`,
		`git log -S x ../peer`,
		`git log -G x -- ../peer`,
		`grep '\.\.' f > ../out`,
		`grep '\.\.' f 2>../out`,
		`tr a b < ../x`,
		`tr a b > ../x`,
		`awk '/x/{print}' ../peer/f`,
		`awk -f ../prog f`,
	})
}

// A glob after the pattern, starting with a plain character, expands only to
// names that start with it, never to an option, so the pattern stays no path.
// With a Windows work root a pattern such as \bLoad\( read as a path is
// rooted at the drive, so the glob once made the line ask only the person
// there and not on Linux. A glob that may expand to an option, or into more
// words at or before a pattern, still leaves every word to be judged as a
// path, so ExecOutside finds the path outside.
func TestGlobAfterThePatternKeepsThePattern(t *testing.T) {
	p := policy(t)
	for _, root := range []string{t.TempDir(), `C:\work`} {
		for _, command := range []string{
			`grep -c -E '\bLoad\(' internal/config/*_test.go; head -3 internal/config/*_test.go | grep -E 'package|==>'`,
			`grep -c -E '\bLoad\(' internal/config/*_test.go`,
			`grep -n '\.Dir' ./internal/*.go`,
			`rg -n '\bx\(' internal/[a-c]*`,
			`git log -G '\bx' -- internal/*.go`,
		} {
			if a, d := searchCase(t, p, root, command); PersonOnly(d) || ExecOutside(a) {
				t.Errorf("%s: %q: %+v; want no person-only ask", root, command, d)
			}
		}
		for _, command := range []string{
			`grep '\bLoad\(' /elsewhere/*.go`,
			`grep '\bLoad\(' ../*.go`,
			`grep /elsewhere/pa* f`,
			`grep -e /elsewhere/pa* f`,
			`grep -A 1* /elsewhere/x f`,
			`grep /elsewhere/x f -*`,
			`grep /elsewhere/x f *`,
			`grep /elsewhere/x f "-"*`,
			`grep /elsewhere/x f a$X*`,
			`grep /elsewhere/x f ~*`,
			`git log -G /elsewhere/pa* x`,
		} {
			command = strings.ReplaceAll(command, "/elsewhere", absoluteTestPath(root, "/elsewhere"))
			if a, _ := searchCase(t, p, root, command); !ExecOutside(a) {
				t.Errorf("%s: %q: not outside", root, command)
			}
		}
	}
}

// A line of search and read commands on paths inside the work root is a
// read; one that ends in a shell or writes stays with the lead or the person.
func TestSearchPipelineIntoShellIsNoRead(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	for _, command := range []string{
		`grep '\.\.' f | sh`,
		`grep x f | head | sh`,
		`cat f | bash`,
		`grep -n x f | tee out`,
		`grep x f > out`,
	} {
		if _, d := searchCase(t, p, root, command); d.Kind == Allow || PersonOnly(d) {
			t.Errorf("%q: %+v; want an ask for the lead", command, d)
		}
	}
	for _, command := range []string{
		`grep -n x f | head -80`,
		`grep -c y f g | sort | uniq -c | wc -l`,
		`head -3 f g | grep -v x | cut -c1-20 | tail -2`,
		`cat f | grep -e .. -e /x`,
	} {
		if _, d := searchCase(t, p, root, command); d.Kind != Allow {
			t.Errorf("%q: %+v; want an allow", command, d)
		}
	}
}

// The line ran with a here-document whose lines the log joined. The separate
// "> /dev/null" made cat seem to read /dev/null, a path outside the work root.
func TestReadOfStreamDeviceIsNotOutside(t *testing.T) {
	// Windows has no /dev, and the gate knows no device name such as NUL, so
	// there only the half that must stay outside is checked. There a rooted
	// path such as /dev/zero is on the current drive, outside the work root.
	if runtime.GOOS != "windows" {
		checkNotPersonOnly(t, []string{
			`cd ROOT/.worktrees/exp-gate-c/ && git stash list | head -2; cat > /dev/null <<'EOF' EOF git log --oneline develop -1 2>&1 | head -2`,
			`cat /dev/null`,
			`cat > /dev/null; ls`,
			`head -c 4 /dev/stdin`,
			`wc -l /dev/fd/3`,
		})
	}
	checkStillOutside(t, []string{
		`cat /elsewhere/dev/null`,
		`cat /dev/zero`,
		`cat /dev/tty`,
		`cat /etc/passwd`,
		`cat > /dev/null; cat ../peer/f`,
		`cat /dev/null ../peer/f`,
	})
}
