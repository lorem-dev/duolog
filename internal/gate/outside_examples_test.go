package gate

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/pathutil"
)

// outsideFixture lays out a project with a linked worktree, a second worktree,
// a folder outside both, and the Claude temp folders of the worker's session
// and of another session.
type outsideFixture struct {
	work, other, outside, temp, otherTemp string
}

func newOutsideFixture(t *testing.T) outsideFixture {
	t.Helper()
	base := t.TempDir()
	f := outsideFixture{
		work:    filepath.Join(base, "duolog", ".worktrees", "golden-a"),
		other:   filepath.Join(base, "duolog", ".worktrees", "peer-gone"),
		outside: filepath.Join(base, "elsewhere"),
	}
	temps := ClaudeTemps(filepath.Join(base, "tmp"), 501, "0019518a-6dc7-4bf8-a704-1eca8307686b", f.work)
	if len(temps) != 1 {
		t.Fatalf("temps: %q", temps)
	}
	f.temp = temps[0]
	f.otherTemp = filepath.Join(filepath.Dir(f.temp), "5c1c2d1e-1292-4483-a206-10c2405a3fb3")
	for _, dir := range []string{
		filepath.Join(f.work, "internal", "workerenv", "testdata", "golden"),
		filepath.Join(f.work, "sub"),
		f.other, f.outside,
		filepath.Join(f.temp, "scratchpad"),
		filepath.Join(f.otherTemp, "scratchpad"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{
		filepath.Join(f.work, "sub", "link"):            f.outside,
		filepath.Join(f.temp, "scratchpad", "escape"):   f.outside,
		filepath.Join(f.work, "sub", "dangling"):        filepath.Join(f.outside, "new"),
		filepath.Join(f.temp, "scratchpad", "dangling"): filepath.Join(f.outside, "new"),
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f outsideFixture) expand(command string) string {
	return strings.NewReplacer("{work}", f.work, "{other}", f.other, "{outside}", f.outside, "{temp}", f.temp, "{othertemp}", f.otherTemp).Replace(command)
}

// The asks a worker sent as outside its work root, from a peer whose work root
// and folder are its own linked worktree. " … " marked text the bus cut; those
// commands are completed here the way they began. Only a path that resolves
// outside the work root, or into another session's temp folder, needs the
// person.
func TestFlaggedAsksInsideTheWorktree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell lines and the Claude temp folder")
	}
	f := newOutsideFixture(t)
	p := policy(t)
	for _, tc := range []struct {
		command string
		outside bool
	}{
		// 1. A file in the worker's own scratchpad.
		{`cat > {temp}/scratchpad/fix.py && go vet ./internal/peer/ 2>&1 | head`, false},
		// 2. A quoted pattern with \| and \. is not a path.
		{`cat internal/config/defaults.go | head -40; grep -n "^func \|cfg\." internal/doctor/doctor.go | head -60; grep -n "yaml" go.mod`, false},
		// 3. cd into the worktree, and a grep pattern that starts with /.
		{`cd {work}/internal/workerenv/testdata/golden && wc -l build_lead_free.golden build_refusals.golden; grep -l "/Users\|/var/folders\|/tmp" *.golden`, false},
		// 4. A sed print script that starts with /.
		{`cd {work}/internal/workerenv/testdata/golden && sed -n '/^signals/,$p' codex_lead_free_observed_prefix.golden codex_worker_free_speed_in_launch.golden`, false},
		// 5. A variable makes the line uncertain; the copy goes to the scratchpad.
		{`f=internal/workerenv/testdata/golden/codex_worker_free_workspace_write.golden && cp "$f" {temp}/scratchpad/orig.golden; echo ok`, false},
		// 6. An awk program with / patterns, and a redirect into the scratchpad.
		{`git diff CHANGES.md | grep '^[+-]' ; git show HEAD:CHANGES.md > {temp}/scratchpad/c.md; awk '/^## Development/{f=1;next} /^## /{f=0} f&&/^- /{n++} END{print n}' CHANGES.md`, false},
		// 7-10. git grep patterns with backslashes.
		{`git grep -n "\.Waiting\b\|SetWaiting\|IsWaiting" -- '*.go' | grep -v "_test.go" ; grep -n "Stop\|events(\|\"SessionEnd\"" internal/install/install_test.go cmd/duolog/install_test.go | head -20`, false},
		{`git grep -n "\.watch(" -- 'internal/peer/*.go' ':!*_test.go'; grep -n "lint-exceptions\|lint-go\|docs-lint" -A6 Makefile | head -50`, false},
		{`git grep -n "^func (r \*Registry)" -- internal/session | cut -c1-160; git grep -n "func Address" -- internal/session`, false},
		{`git grep -n "func ReportPath\|func (e \*env) taskEnded\|func pendingAsk\|func Send(\|func List(" -- 'internal/*.go'; ls docs; git grep -n "Stop hook\|turn ends\|turn end" -- docs | head`, false},
		// 11. A listing of the worker's own scratchpad.
		{`git status --short; ls {temp}/scratchpad/ 2>&1`, false},
		// 12. An awk program with \/ in it.
		{`go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./internal/... 2>/dev/null | awk '{printf "%s:", $1; for(i=2;i<=NF;i++) if ($i ~ /^internal\//) printf " %s", substr($i,10); print ""}'`, false},
		// 13. A loop; nothing outside.
		{`go test -count=1 ./internal/delegate/ -run Golden -update 2>&1|tail -2; cd internal/delegate && for i in 1 2 3; do go test -count=1 ./internal/delegate/ ./internal/workerenv/ 2>&1 | tail -3; done`, false},
		// 14. \s in a grep -E pattern.
		{`go test -count=1 ./internal/workerenv/ -run 'TestBuildGolden/codex_worker_free_read_only' 2>&1 | grep -E "^(--- |\s+--- |FAIL|ok)"`, false},
		// 15. A loop variable; nothing outside is named.
		{`go test ./internal/gate/ ./internal/hook/ ./internal/install/ 2>&1 | tail -5; for f in a b; do cp $f bak/$f; done; go test -count=1 ./internal/install/ 2>&1 | tail -2; git status --short`, false},
		// 16. \{8,\} in a grep pattern.
		{`go test ./internal/install/ 2>&1 | grep -v "^ \{8,\}" | tail -30`, false},
		// 17. A grep pattern that starts with /.
		{`go test ./internal/workerenv/ -run Golden -update -count=1 2>&1 | tail -5; sed -n 1,5p internal/workerenv/testdata/golden/a.golden; grep -rl "/Users\|/var/folders\|private/tmp" internal/*/testdata/golden`, false},
		// 18. The clear case.
		{`go version && grep -n "^go \|^module" go.mod && sed -n 1,80p Makefile`, false},
		// 19-34. grep patterns with \|, \*, \( and \. in them.
		{`grep -n '^func \|^type \|^var \|^const ' internal/telemetry/telemetry_dependencies_test.go internal/telemetry/ | grep -i dep; find . -name '*dependencies_test.go' -not -path './.worktrees/*'`, false},
		{`grep -n 'AllowManualModel &&\|ambiguous model mapping' internal/router/router.go cmd/duolog/models.go; grep -n 'func pick\|func stuckInput\|func operationOutcome' internal/peer/*.go`, false},
		{`grep -n 'maxLine\|func resultLine\|func finalLine\|func Run(\|WithOptions' internal/delegate/delegate.go | head -20; ls internal/delegate internal/peer cmd/duolog | head -120 | tr '\n' ' '`, false},
		{`grep -n "^## \|^### " .superpowers/tasks/design-b.md | head -50; sed -n 280,330p internal/delegate/delegate.go; grep -n "maxLine" internal/delegate/*.go | head -20`, false},
		{`grep -n "^func \|^// [A-Z]\|Stop\|turn" internal/peer/peer.go | head -150`, false},
		{`grep -n "^func \|^type \|^var \|^const " internal/peer/peer.go | sed -n '30,200p'`, false},
		{`grep -n "func \|RexSession\|REX_SESSION" internal/peer/peer.go | head -150`, false},
		{`grep -n "func \|timeout\|Timeout" internal/gate/ask_test.go | head -60`, false},
		{`grep -n "peer" .golangci.yml; grep -n -A6 "^lint-go\|^lint-exceptions" Makefile; grep -n "^func \|^type \|^var \|^const " internal/peer/peer.go`, false},
		{`grep -n "resultLine(\|out.Line\|Line = \|Line:" internal/delegate/delegate.go; grep -n "x" internal/delegate/migration_test.go; grep -n "func authHarness" -A25 internal/delegate/auth_test.go`, false},
		{`grep -n "type Lead struct" -A30 internal/session/session.go; grep -n "func (r \*Registry) Lead(" -A40 internal/session/session.go; grep -n "list" cmd/duolog/peer.go | head -20`, false},
		{`grep -n "Wait(ctx\|Wait(context" internal/peer/*_test.go | head -10; grep -n "^func [a-z][A-Za-z]*(t \*testing.T" internal/peer/peer_test.go | head -30`, false},
		{`grep -nE '^func (Run|Resolve|ReadLeadState|ScanEnv|Bridge|NewForProcess|GateBinary)' internal/quota/quota.go internal/auth/auth.go | head -20; sed -n 24,40p internal/models/models.go`, false},
		{`grep -rn "func (e \*env) clientFor" -A25 internal/peer/*.go; grep -n "func " internal/peer/peer.go; grep -n "func (r \*Registry) ResolveLead" -B3 -A50 internal/session/*.go | head -120`, false},
		{`grep -rn "peer\.Close(\|[^a-zA-Z.]Close(r\.ctx\|Close(ctx, " --include=*.go . | head; sed -n 105,125p docs/rex.md; sed -n 25,60p CHANGES.md; ls .superpowers/tasks`, false},
		{`grep -rn "Stop\b\|\"Stop\"\|SubagentStop\|turn-end\|TurnEnd\|turnEnd" --include=*.go . | grep -v "ticker.Stop\|timer.Stop\|\.Stop()" | head -30; grep -n "case \"" cmd/duolog/hook.go`, false},
		// 35-36. The Go source tree is outside.
		{`ls /opt/homebrew/opt/go/libexec/src/os`, true},
		{`ls /usr/local/go/src/os`, true},
		// 37. The worker's own scratchpad, then its own worktree.
		{`mkdir -p {temp}/scratchpad && cd {work} && go version && grep -n "^go \|^module" go.mod && cat Makefile | head -80`, false},
		// 38-40. The Go source tree is outside.
		{`rg -n -A 50 'func remove' /opt/homebrew/opt/go/libexec/src/internal/syscall/windows/at_windows.go`, true},
		{`rg -n -A 60 'func Remove' /opt/homebrew/opt/go/libexec/src/os/file_windows.go`, true},
		{`rg -n -A 65 'func rootRemove' /opt/homebrew/opt/go/libexec/src/os/root_windows.go`, true},
	} {
		command := f.expand(tc.command)
		a := Action{Kind: "exec", Target: command, Role: "implement", Work: f.work, Cwd: f.work, Temp: []string{f.temp}}
		if got := ExecOutside(a); got != tc.outside {
			t.Errorf("%s: outside=%v, want %v", tc.command, got, tc.outside)
		}
		d := Evaluate(p, LeadRules{}, mode.Ask, a)
		if PersonOnly(d) != tc.outside || tc.outside && d.Kind != Ask {
			t.Errorf("%s: %+v, person=%v", tc.command, d, tc.outside)
		}
	}
}

// What really leaves the work root still needs the person, however it is
// spelled.
func TestOutsidePathsStillNeedThePerson(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell lines and the Claude temp folder")
	}
	f := newOutsideFixture(t)
	p := policy(t)
	for _, tc := range []struct {
		command string
		outside bool
	}{
		{`grep -n x {other}/go.mod`, true},
		{`cat ../peer-gone/go.mod`, true},
		{`cat ~/notes`, true},
		{`cat ~root/notes`, true},
		{`cat \/etc/hosts`, true},
		{`cat "{outside}/a b"`, true},
		{`grep -f {outside}/patterns go.mod`, true},
		{`grep -e x {outside}/a`, true},
		{`grep x -- {outside}/a`, true},
		{`rg --files {outside}`, true},
		{`git grep -n x -- {outside}`, true},
		{`git -C {other} grep x`, true},
		{`sed -n '1,5p' {outside}/a`, true},
		{`sed 's/a/b/w {outside}/out' go.mod`, true},
		{`sed -n -e '/x/w {outside}/out' go.mod`, true},
		{`awk '{print > "{outside}/out"}' go.mod`, true},
		{`awk -f {outside}/prog.awk go.mod`, true},
		{`sh -c 'cat {outside}/a'`, true},
		{`bash -c "cat {outside}/a"`, true},
		{`cat sub/link/a`, true},
		{`cd sub && cat link/a`, true},
		{`cd .. && cat x`, true},
		{`cd {outside} && cat x`, true},
		{`cd sub | cat ../../x`, true},
		{`cat > sub/dangling`, true},
		{`cat > {temp}/scratchpad/dangling`, true},
		{`cat {temp}/scratchpad/escape/a`, true},
		{`cat {temp}/../5c1c2d1e-1292-4483-a206-10c2405a3fb3/scratchpad/a`, true},
		{`cat {othertemp}/scratchpad/a`, true},
		{`ls {temp}/..`, true},
		{`cp go.mod {outside}/ 2>/dev/null`, true},
		{`go test ./... > {outside}/log`, true},
		{`go test ./... 2> {outside}/log`, true},
		{`wc -l < {outside}/a`, true},
		{`if grep -q x go.mod; then cat {outside}/a; fi`, true},
		{`cat C:\Users\x`, true},
		{`grep x <<< "{outside}/a" && cat {outside}/b`, true},
		// Inside, or no file at all.
		{`grep x <<< "{outside}/a"`, false},
		{`grep -n x go.mod 2>/dev/null`, false},
		{`cat sub/a\ b`, false},
		{`cat 'sub/a\b'`, false},
		// Judged from both folders: cd may fail, and cat then reads from the work root.
		{`cd sub && cat ../go.mod`, true},
		{`ls {temp}/scratchpad`, false},
		{`ls {temp}`, false},
		{`git log --grep '/x\|/y'`, false},
		{`rg -e '/x' -e '/y' internal`, false},
		{`sed -n '/^a/,/^b/p' go.mod`, false},
		{`xargs grep '/etc/x' < files.txt`, false},
	} {
		command := f.expand(tc.command)
		a := Action{Kind: "exec", Target: command, Role: "implement", Work: f.work, Cwd: f.work, Temp: []string{f.temp}}
		if got := ExecOutside(a); got != tc.outside {
			t.Errorf("%s: outside=%v, want %v", tc.command, got, tc.outside)
		}
		d := Evaluate(p, LeadRules{}, mode.Ask, a)
		if PersonOnly(d) != tc.outside || tc.outside && d.Kind != Ask {
			t.Errorf("%s: %+v, person=%v", tc.command, d, tc.outside)
		}
	}
}

// Words inside a commit message or other quoted text are not commands, so
// they do not count as duolog telemetry or duolog balance; a quoted script
// that a shell runs still does.
func TestStoreCommandIgnoresQuotedProse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell lines")
	}
	for _, tc := range []struct {
		command string
		store   bool
	}{
		{`git commit -m "fix: keep telemetry and balance data for the person; duolog remember asks"`, false},
		{`git commit -m "docs: duolog telemetry and duolog balance stay with the person"`, false},
		{`git commit -m 'duolog telemetry show' -m "duolog balance reset"`, false},
		{`git tag -a v1 -m "duolog telemetry"`, false},
		{`gh pr create --title x --body "run duolog telemetry status to see it"`, false},
		{`echo "duolog telemetry show" > notes.txt`, false},
		{`GIT_EDITOR=true git commit -m "duolog telemetry"`, false},
		{`git commit -m "duolog telemetry" && duolog telemetry show`, true},
		{`git commit -m "x"; duolog balance reset`, true},
		{`echo x | duolog telemetry prune`, true},
		{`sh -c 'duolog telemetry show'`, true},
		{`sh -c 'true;duolog telemetry show'`, true},
		{`bash -c "git commit -m 'duolog telemetry'"`, true},
		{`eval "duolog telemetry show"`, true},
		{`watch git commit -m "x; duolog telemetry show"`, true},
		{`git -c core.editor="duolog telemetry show" commit`, true},
		{`git rebase --exec "duolog telemetry show" main`, true},
		{`duolog\ telemetry show`, true},
		{`"duo""log" telemetry show`, true},
		{`duolog --lead L telemetry show`, true},
	} {
		if got := protectedCommand(tc.command, ""); got != tc.store {
			t.Errorf("%s: store command=%v, want %v", tc.command, got, tc.store)
		}
	}
}

// Claude keeps a session's scratchpad and task output under
// <tmp>/claude-<uid>/<start folder, every other character than a letter or
// digit turned into ->/<session>.
func TestClaudeTemps(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Claude's temp folder on POSIX")
	}
	base := pathutil.Resolved(t.TempDir())
	got := ClaudeTemps(base, 501, "0019518a-6dc7", "/srv/duolog/.worktrees/golden-a", "/srv/duolog/.worktrees/golden-a", "/srv/x_y")
	want := []string{
		filepath.Join(base, "claude-501", "-srv-duolog--worktrees-golden-a", "0019518a-6dc7"),
		filepath.Join(base, "claude-501", "-srv-x-y", "0019518a-6dc7"),
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %q, want %q", got, want)
	}
	for _, id := range []string{"", "../x", "a/b"} {
		if got := ClaudeTemps(base, 501, id, "/srv/x"); got != nil {
			t.Errorf("session %q: %q", id, got)
		}
	}
	if got := ClaudeTemps(base, 501, "abc", ""); got != nil {
		t.Errorf("no folder: %q", got)
	}
}

// A read command that names the worker's own temp folder reads that session's
// files, which the worker wrote; another session's folder still asks.
func TestOwnTempReadsAreAllowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Claude's temp folder on POSIX")
	}
	f := newOutsideFixture(t)
	p := policy(t)
	for _, tc := range []struct {
		command, want string
		person        bool
	}{
		{"cat {temp}/scratchpad/a.txt", Allow, false},
		{"ls {temp}/scratchpad", Allow, false},
		{"cat {othertemp}/scratchpad/a.txt", Ask, true},
		{"cat {temp}/scratchpad/escape/a", Ask, true},
	} {
		a := Action{Kind: "exec", Target: f.expand(tc.command), Role: "implement", Work: f.work, Cwd: f.work, Temp: []string{f.temp}}
		d := Evaluate(p, LeadRules{}, mode.Edits, a)
		if d.Kind != tc.want || PersonOnly(d) != tc.person {
			t.Errorf("%s: %+v", tc.command, d)
		}
	}
	// The hook marks a read tool's target in the own temp folder as OwnResult.
	for target, want := range map[string]bool{
		filepath.Join(f.temp, "scratchpad", "a.txt"):              true,
		f.temp:                                       true,
		filepath.Join(f.temp, "scratchpad", "escape", "a"):         false,
		filepath.Join(f.temp, "scratchpad", "dangling"):            false,
		filepath.Join(f.temp, "..", filepath.Base(f.otherTemp), "a"): false,
		filepath.Join(f.otherTemp, "scratchpad", "a.txt"):         false,
		filepath.Dir(f.temp):                                      false,
		"scratchpad/a.txt":                                        false,
	} {
		if got := InTemp([]string{f.temp}, target); got != want {
			t.Errorf("InTemp(%s) = %v, want %v", target, got, want)
		}
	}
}
