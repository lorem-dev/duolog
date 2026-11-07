package gate

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/mode"
)

// outsideFixture lays out a project with a linked worktree, a second worktree,
// a folder outside both, and the Claude temp folders of the worker's session
// and of another session.
type outsideFixture struct {
	work, other, outside, temp, otherTemp string
}

func newOutsideFixture(t *testing.T) outsideFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell lines and the Claude temp folder")
	}
	base := t.TempDir()
	f := outsideFixture{
		work:    filepath.Join(base, "duolog", ".worktrees", "golden-a"),
		other:   filepath.Join(base, "duolog", ".worktrees", "peer-gone"),
		outside: filepath.Join(base, "elsewhere"),
	}
	f.temp = ClaudeTemp(filepath.Join(base, "tmp"), 501, ownSession, f.work)
	if f.temp == "" {
		t.Fatal("no temp folder")
	}
	f.otherTemp = filepath.Join(filepath.Dir(f.temp), otherSession)
	for _, dir := range []string{
		filepath.Join(f.work, "internal", "workerenv", "testdata", "golden"),
		filepath.Join(f.work, "sub"),
		filepath.Join(f.work, "a", "b"),
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
		// Dangling inside the work root where it stands, but copied into the
		// temp folder it names another session's file.
		filepath.Join(f.work, "a", "b", "l"): "../../" + otherSession + "/scratchpad/secret",
		// A .. after a symlink goes up from the symlink's target.
		filepath.Join(f.work, "sub", "dd"): "link/../newfile",
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{filepath.Join(base, "secret"), filepath.Join(f.otherTemp, "scratchpad", "secret"), filepath.Join(f.temp, "scratchpad", "f")} {
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f outsideFixture) expand(command string) string {
	return strings.NewReplacer("{work}", f.work, "{other}", f.other, "{outside}", f.outside, "{temp}", f.temp, "{othertemp}", f.otherTemp).Replace(command)
}

// checkOutside runs each command as a worker in its own worktree, with its
// own Claude temp folder, and checks both ExecOutside and that only an outside
// command reaches the person's ask.
func checkOutside(t *testing.T, f outsideFixture, cases []outsideCase) {
	t.Helper()
	p := testPolicy(t)
	for _, tc := range cases {
		command := f.expand(tc.command)
		a := Action{Kind: "exec", Target: command, Role: "implement", Work: f.work, Cwd: f.work, State: f.work, Temp: []string{f.temp}}
		if got := ExecOutside(a); got != tc.outside {
			t.Errorf("%s: outside=%v, want %v", tc.command, got, tc.outside)
		}
		for _, m := range []mode.Mode{mode.Ask, mode.Edits} {
			d := Evaluate(p, LeadRules{}, m, a)
			if PersonOnly(d) != tc.outside || tc.outside && d.Kind != Ask {
				t.Errorf("%s in %s: %+v, person=%v", tc.command, m, d, tc.outside)
			}
		}
	}
}

type outsideCase struct {
	command string
	outside bool
}

// The asks a worker sent as outside its work root, from a peer whose work root
// and folder are its own linked worktree. Only a path that resolves outside
// the work root, or into another session's temp folder, needs the person.
func TestFlaggedAsksInsideTheWorktree(t *testing.T) {
	f := newOutsideFixture(t)
	checkOutside(t, f, []outsideCase{
		// A file in the worker's own scratchpad.
		{`cat > {temp}/scratchpad/fix.py && go vet ./internal/peer/ 2>&1 | head`, false},
		// A quoted pattern with \| and \. is not a path.
		{`cat internal/config/defaults.go | head -40; grep -n "^func \|cfg\." internal/doctor/doctor.go | head -60; grep -n "yaml" go.mod`, false},
		// cd into the worktree, and a grep pattern that starts with /. A glob
		// may expand to an option such as -e, which makes the pattern a file
		// to read, so the pattern counts as a path again (round 4).
		{`cd {work}/internal/workerenv/testdata/golden && wc -l build_lead_free.golden build_refusals.golden; grep -l "/Users\|/var/folders\|/tmp" *.golden`, true},
		{`cd {work}/internal/workerenv/testdata/golden && wc -l build_lead_free.golden build_refusals.golden; grep -l "/Users\|/var/folders\|/tmp" a.golden b.golden`, false},
		// A sed print script that starts with /.
		{`cd {work}/internal/workerenv/testdata/golden && sed -n '/^signals/,$p' codex_lead_free_observed_prefix.golden codex_worker_free_speed_in_launch.golden`, false},
		// A variable makes the line uncertain; the copy goes to the scratchpad.
		{`f=internal/workerenv/testdata/golden/codex_worker_free_workspace_write.golden && cp "$f" {temp}/scratchpad/orig.golden; echo ok`, false},
		// An awk program with / patterns, and a redirect into the scratchpad.
		{`git diff CHANGES.md | grep '^[+-]' ; git show HEAD:CHANGES.md > {temp}/scratchpad/c.md; awk '/^## Development/{f=1;next} /^## /{f=0} f&&/^- /{n++} END{print n}' CHANGES.md`, false},
		// git grep patterns with backslashes.
		{`git grep -n "\.Waiting\b\|SetWaiting\|IsWaiting" -- '*.go' | grep -v "_test.go" ; grep -n "Stop\|events(\|\"SessionEnd\"" internal/install/install_test.go cmd/duolog/install_test.go | head -20`, false},
		{`git grep -n "\.watch(" -- 'internal/peer/*.go' ':!*_test.go'; grep -n "lint-exceptions\|lint-go\|docs-lint" -A6 Makefile | head -50`, false},
		{`git grep -n "^func (r \*Registry)" -- internal/session | cut -c1-160; git grep -n "func Address" -- internal/session`, false},
		{`git grep -n "func ReportPath\|func (e \*env) taskEnded\|func pendingAsk\|func Send(\|func List(" -- 'internal/*.go'; ls docs; git grep -n "Stop hook\|turn ends\|turn end" -- docs | head`, false},
		// A listing of the worker's own scratchpad.
		{`git status --short; ls {temp}/scratchpad/ 2>&1`, false},
		// An awk program with \/ in it.
		{`go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./internal/... 2>/dev/null | awk '{printf "%s:", $1; for(i=2;i<=NF;i++) if ($i ~ /^internal\//) printf " %s", substr($i,10); print ""}'`, false},
		// A loop; nothing outside.
		{`go test -count=1 ./internal/delegate/ -run Golden -update 2>&1|tail -2; cd internal/delegate && for i in 1 2 3; do go test -count=1 ./internal/delegate/ ./internal/workerenv/ 2>&1 | tail -3; done`, false},
		// \s in a grep -E pattern.
		{`go test -count=1 ./internal/workerenv/ -run 'TestBuildGolden/codex_worker_free_read_only' 2>&1 | grep -E "^(--- |\s+--- |FAIL|ok)"`, false},
		// A loop variable; nothing outside is named.
		{`go test ./internal/gate/ ./internal/hook/ ./internal/install/ 2>&1 | tail -5; for f in a b; do cp $f bak/$f; done; go test -count=1 ./internal/install/ 2>&1 | tail -2; git status --short`, false},
		// \{8,\} in a grep pattern.
		{`go test ./internal/install/ 2>&1 | grep -v "^ \{8,\}" | tail -30`, false},
		// A grep pattern that starts with /. The line as flagged ended in the
		// glob internal/*/testdata/golden, which now makes the pattern count
		// as a path (round 4).
		{`go test ./internal/workerenv/ -run Golden -update -count=1 2>&1 | tail -5; sed -n 1,5p internal/workerenv/testdata/golden/a.golden; grep -rl "/Users\|/var/folders\|private/tmp" internal/workerenv/testdata/golden`, false},
		// The clear case.
		{`go version && grep -n "^go \|^module" go.mod && sed -n 1,80p Makefile`, false},
		// grep patterns with \|, \*, \( and \. in them.
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
		// The Go source tree is outside.
		{`ls /opt/homebrew/opt/go/libexec/src/os`, true},
		{`ls /usr/local/go/src/os`, true},
		// The worker's own scratchpad, then its own worktree.
		{`mkdir -p {temp}/scratchpad && cd {work} && go version && grep -n "^go \|^module" go.mod && cat Makefile | head -80`, false},
		{`rg -n -A 50 'func remove' /opt/homebrew/opt/go/libexec/src/internal/syscall/windows/at_windows.go`, true},
		{`rg -n -A 60 'func Remove' /opt/homebrew/opt/go/libexec/src/os/file_windows.go`, true},
		{`rg -n -A 65 'func rootRemove' /opt/homebrew/opt/go/libexec/src/os/root_windows.go`, true},
	})
}

// What really leaves the work root still needs the person, however it is
// spelled.
func TestOutsidePathsStillNeedThePerson(t *testing.T) {
	f := newOutsideFixture(t)
	checkOutside(t, f, []outsideCase{
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
		{`cd sub && echo x > link/b`, true},
		{`cd .. && cat x`, true},
		{`cd {outside} && cat x`, true},
		{`cd sub | cat ../../x`, true},
		{`cat > sub/dangling`, true},
		{`echo x > sub/dangling`, true},
		{`cat > {temp}/scratchpad/dangling`, true},
		{`echo x > {temp}/scratchpad/escape/a`, true},
		{`mkdir -p {temp}/scratchpad/escape/x`, true},
		{`cp go.mod {temp}/scratchpad/dangling`, true},
		{`cat {temp}/scratchpad/escape/a`, true},
		{`cat {temp}/../` + otherSession + `/scratchpad/a`, true},
		{`echo x > {temp}/../` + otherSession + `/scratchpad/a`, true},
		{`echo x > {temp}/scratchpad/../../x`, true},
		{`cat {othertemp}/scratchpad/a`, true},
		{`echo x > {othertemp}/scratchpad/a`, true},
		{`mkdir -p {othertemp}/scratchpad/x`, true},
		// A file there may hold code that a later command runs, so a read
		// counts only when what it prints stays on screen or in the folder.
		{`echo x > {temp}/scratchpad/a.py && python3 {temp}/scratchpad/a.py`, true},
		{`sh < {temp}/scratchpad/a.sh`, true},
		{`tail {temp}/scratchpad/f | sh`, true},
		{`cat {temp}/scratchpad/f | head -3 | bash`, true},
		{`cat {temp}/scratchpad/f > x.sh && go vet ./...`, true},
		{`cat {temp}/scratchpad/f | tee x.sh`, true},
		{`cat {temp}/scratchpad/new && go vet ./...`, true},
		{`cat {temp}/scratchpad/a.sh | sh`, true},
		{`cat < {temp}/scratchpad/a.sh | sh`, true},
		// A pipe that the next line continues.
		{"go vet; cat {temp}/scratchpad/f |\nsh", true},
		{"cat {temp}/scratchpad/f |\n\nsh", true},
		{"cat {temp}/scratchpad/f |\n  bash -s", true},
		{"head -1 {temp}/scratchpad/f | head |\npython3", true},
		{"cat {temp}/scratchpad/f | sort | uniq -c |\nsh", true},
		{"cat {temp}/scratchpad/f |\ntee x.sh", true},
		{"cat {temp}/scratchpad/f | \\\nsh", true},
		{"cat {temp}/scratchpad/f |&\nsh", true},
		// The worker names the files in the folder, so what ls prints of it
		// may run as code too.
		{`go vet; ls {temp}/scratchpad | sh`, true},
		{"ls {temp}/scratchpad |\nsh", true},
		{`ls {temp}/scratchpad | while read f; do $f; done`, true},
		{`ls {temp}/scratchpad | xargs rm`, true},
		{`ls {temp}/scratchpad > x.sh`, true},
		{`ls {temp}/scratchpad | tee x.sh`, true},
		{`ls {temp} | sh`, true},
		// A command substitution, exec, eval, source, the . command, process
		// substitution and a here-document can turn what a temp read prints
		// into code or send it into a file, so a longer line with one of them
		// gets no temp path as the worker's own (round 3).
		{`x=$(cat {temp}/scratchpad/f); sh -c "$x"`, true},
		{`x="$(cat {temp}/scratchpad/f)"; $x`, true},
		{`x=$(ls {temp}/scratchpad); $x`, true},
		{`x=$(cat {temp}/scratchpad/f); echo "$x" > x.sh; sh x.sh`, true},
		{`x=$(cat {temp}/scratchpad/f) sh -c '$x'`, true},
		{`x=$(cat {temp}/scratchpad/f); go vet $x`, true},
		{"x=`cat {temp}/scratchpad/f`; $x", true},
		{`x=$(cat {outside}/f)`, true},
		{`x=$(cat {outside}/f); sh -c "$x"`, true},
		{`x=$(go env GOPATH); echo $x`, false},
		{`go vet; diff <(cat {temp}/scratchpad/f) go.mod`, true},
		{`cat {temp}/scratchpad/f > >(sh)`, true},
		{`exec > x.sh; cat {temp}/scratchpad/f; sh x.sh`, true},
		{`exec >x.sh; ls {temp}/scratchpad; sh x.sh`, true},
		{`exec >>x.sh 2>&1; cat {temp}/scratchpad/f`, true},
		{`exec 1<>x.sh; cat {temp}/scratchpad/f`, true},
		{`exec 3>x.sh; cat {temp}/scratchpad/f >&3; sh x.sh`, true},
		{`command exec >x.sh; cat {temp}/scratchpad/f`, true},
		{`{ cat {temp}/scratchpad/f >&3; } 3>x.sh`, true},
		{`eval x; cat {temp}/scratchpad/f`, true},
		{`source x.sh; ls {temp}/scratchpad`, true},
		{`. ./x.sh; ls {temp}/scratchpad`, true},
		{`go vet; echo x > {temp}/scratchpad/a; . {temp}/scratchpad/a`, true},
		{"cat > {temp}/scratchpad/x <<'EOF'\nhi\nEOF", true},
		{"mkdir -p {temp}/scratchpad/d; cat <<EOF\nhi\nEOF", true},
		{`cp {temp}/scratchpad/a go.mod`, true},
		{`cp -t {temp}/scratchpad go.mod`, true},
		{`cp go.mod {temp}/scratchpad/`, true},
		{`ls -R {temp}`, true},
		{`ls -L {temp}/scratchpad`, true},
		{`ls {temp}/scratchpad/escape`, true},
		{`ls {othertemp}`, true},
		{`echo x > {temp}/scratchpad/a\b`, true},
		{`echo x > {temp}/scratchpad/$X`, true},
		// A wrapper such as xargs adds operands after cp's last word, mkdir's
		// and ls's: only the part's own program counts.
		{`xargs cp {temp}/scratchpad/f < list.txt`, true},
		{`find . -print0 | xargs -0 cp {temp}/scratchpad/f`, true},
		{`cat list.txt | xargs cp {temp}/scratchpad/f`, true},
		{`xargs mkdir -p {temp}/scratchpad/d < dirs.txt`, true},
		{`xargs ls {temp} < dirs.txt`, true},
		{`parallel cp go.mod {temp}/scratchpad/x ::: a`, true},
		{`timeout 5 cp go.mod {temp}/scratchpad/x`, true},
		{`timeout 5 ls {temp}`, true},
		{`env ls {temp}/scratchpad`, true},
		{`nice mkdir {temp}/scratchpad/x`, true},
		{`env cat > {temp}/scratchpad/x`, false},
		{`find . -exec cp {} {temp}/scratchpad/x +`, true},
		// cp that can copy a symlink into the folder, and a write that an
		// earlier part of the line may have redirected.
		{`cp -P a/b/l {temp}/scratchpad/w && echo pwn > {temp}/scratchpad/w`, true},
		{`mkdir -p {temp}/scratchpad/q && cp -R a/b {temp}/scratchpad/q && echo x > {temp}/scratchpad/q/b/l`, true},
		{`cp -P a/b/l {temp}/scratchpad/l`, true},
		{`cp -a a/b {temp}/scratchpad/q`, true},
		{`cp -r a/b {temp}/scratchpad/q`, true},
		{`cp -d a/b/l {temp}/scratchpad/l`, true},
		{`cp -s go.mod {temp}/scratchpad/l`, true},
		{`cp --no-dereference a/b/l {temp}/scratchpad/l`, true},
		{`cp --preserve=all go.mod {temp}/scratchpad/l`, true},
		// A quoted pattern with a blank or a leading - is one word.
		{`timeout 5 grep 'x -e' {outside}/f`, true},
		{`xargs grep "x -e" {outside}/f < list.txt`, true},
		{`env rg 'x -e' {outside}/f`, true},
		{`nice grep 'a --regexp' {outside}/f`, true},
		// A wrapper such as xargs can add options after the words, so -e can
		// give grep, rg or sed another pattern or script and the word the gate
		// took for one becomes a file to read: behind a wrapper every word is
		// a path (round 3).
		{`printf -- '-e\nx\n' | xargs grep {outside}/secret`, true},
		{`echo -e x | xargs rg {outside}/secret`, true},
		{`echo -e x | xargs -- grep -n {outside}/secret`, true},
		{`xargs grep -- {outside}/f < list.txt`, true},
		{`xargs grep -e {outside}/f < list.txt`, true},
		{`xargs grep '/etc/x' < files.txt`, true},
		{`timeout 5 grep -e '/x' go.mod`, true},
		{`printf -- '-e\np\n' | xargs sed -n {outside}/secret`, true},
		{`xargs sed -n '/^a/p' < files.txt`, true},
		{`env awk '/x/' go.mod`, true},
		// A variable, a glob or a ~ can expand to an option such as -e, which
		// turns the word the gate took for the pattern into a file to read, so
		// then every word is a path (round 4).
		{`P=-ex; grep {outside}/secret ${P}`, true},
		{`grep -n {outside}/secret ${P:-}`, true},
		{`P=-ex; rg {outside}/secret ${P}`, true},
		{`P=-ex; grep {outside}/secret ${P} | head`, true},
		{`grep {outside}/secret $P`, true},
		{`grep {outside}/secret "$P"`, true},
		{`grep {outside}/secret *`, true},
		{`grep {outside}/secret ?x`, true},
		{`grep {outside}/secret [-]ex`, true},
		{`grep -e {outside}/secret $P`, true},
		{`git log $P --grep {outside}/secret`, true},
		{`git grep {outside}/secret $P`, true},
		// A .. after a symlink goes up from where the symlink leads.
		{`cat sub/link/../secret`, true},
		{`cd -P sub/link/.. && cat secret`, true},
		{`cd sub/link && cat ../secret`, true},
		{`echo x > sub/dd`, true},
		// The script a shell runs is a path the check reads.
		{`sh {outside}/x.sh`, true},
		{`bash {outside}/x.sh`, true},
		{`env bash -x {outside}/x.sh`, true},
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
		{`ls -la {temp}/scratchpad`, false},
		{`ls {temp}/scratchpad | grep x; go vet ./...`, false},
		{`ls {temp}/scratchpad > {temp}/scratchpad/list`, false},
		{"cat {temp}/scratchpad/f |\nhead -1", false},
		{"cat {temp}/scratchpad/f ||\nsh", false},
		{`echo x > "{temp}/scratchpad/a" && go vet ./...`, false},
		{`go test ./... > {temp}/scratchpad/log 2>&1; tail -5 go.mod`, false},
		{`mkdir -p {temp}/scratchpad/a/b && go vet ./...`, false},
		{`cp go.mod {temp}/scratchpad/go.mod && go vet ./...`, false},
		{`cp -p go.mod {temp}/scratchpad/go.mod && go vet ./...`, false},
		// A plain file, a real folder and a read leave no symlink behind, so
		// another command of the line that names the path keeps it the
		// worker's own.
		{`go test > {temp}/scratchpad/log; tail {temp}/scratchpad/log`, false},
		{`go test ./... > {temp}/scratchpad/log 2>&1; tail -5 {temp}/scratchpad/log | grep FAIL`, false},
		{`mkdir -p {temp}/scratchpad/n && echo x > {temp}/scratchpad/n/f`, false},
		{`cp go.mod {temp}/scratchpad/m; cp go.sum {temp}/scratchpad/m`, false},
		{`touch {temp}/scratchpad/t && echo x >> {temp}/scratchpad/t`, false},
		{`cat {temp}/scratchpad/f && go vet ./...`, false},
		{`cat {temp}/scratchpad/f > /dev/null; go vet ./...`, false},
		{`grep -c x {temp}/scratchpad/f | head -1; go vet ./...`, false},
		{`cat sub/../go.mod`, false},
		{`sh scripts/x.sh`, false},
		{`git log --grep '/x\|/y'`, false},
		{`rg -e '/x' -e '/y' internal`, false},
		{`sed -n '/^a/,/^b/p' go.mod`, false},
		{`grep -e '/x' go.mod`, false},
		{`grep '/x$' go.mod`, false},
		{`grep -E "/x|b$" go.mod`, false},
		{`grep -n x *.mod`, false},
		{`awk '/x/' go.mod`, false},
		// A plain single command, and a longer line with none of the
		// constructs above, keep the temp folder the worker's own.
		{`cat {temp}/scratchpad/f`, false},
		{`cat {temp}/scratchpad/f >> {temp}/scratchpad/g`, false},
		{`echo x > {temp}/scratchpad/a; cat {temp}/scratchpad/a | grep x`, false},
		{`go vet ./... > {temp}/scratchpad/vet 2>&1; ls {temp}/scratchpad`, false},
	})
}

// A read or write tool's path is placed as the hook places it. A .. after a
// symlink goes up from where the symlink leads, so sub/link/../secret, with
// sub/link leading out, reads beside the link's target.
func TestToolPathDotDotAfterSymlink(t *testing.T) {
	f := newOutsideFixture(t)
	p := testPolicy(t)
	for _, tc := range []struct {
		kind, target string
		person       bool
	}{
		{"read", "sub/link/../secret", true},
		{"write", "sub/link/../secret", true},
		{"read", filepath.Join(f.work, "sub", "link") + "/../secret", true},
		{"write", "sub/dd", true},
		{"read", "sub/../go.mod", false},
		{"read", "a/b/../../go.mod", false},
	} {
		target := RelativeTarget(f.work, f.work, tc.target)
		for _, m := range []mode.Mode{mode.Ask, mode.Edits, mode.Full} {
			d := Evaluate(p, LeadRules{}, m, Action{Kind: tc.kind, Target: target, Role: "implement", Work: f.work})
			if PersonOnly(d) != tc.person || d.Kind == Allow && tc.person {
				t.Errorf("%s %s (%s) in %s: %+v", tc.kind, tc.target, target, m, d)
			}
		}
	}
}

// What a read or ls of the worker's own temp folder prints may run as code
// when it goes into a program or a file, so full mode allows none of it and
// the person answers, also when the pipe continues on the next line.
func TestTempOutputIntoCodeNeedsThePersonInFullMode(t *testing.T) {
	f := newOutsideFixture(t)
	p := testPolicy(t)
	for _, command := range []string{
		"cat {temp}/scratchpad/f |\nsh",
		"go vet; cat {temp}/scratchpad/f |\n\nsh",
		"cat {temp}/scratchpad/f |\ntee x.sh",
		`go vet; ls {temp}/scratchpad | sh`,
		"ls {temp}/scratchpad |\nsh",
		`ls {temp}/scratchpad | xargs rm`,
		`ls {temp}/scratchpad > x.sh`,
		`ls {temp}/scratchpad | tee x.sh`,
		`x=$(cat {temp}/scratchpad/f); sh -c "$x"`,
		`x=$(ls {temp}/scratchpad); $x`,
		`exec > x.sh; cat {temp}/scratchpad/f; sh x.sh`,
		`exec 3>x.sh; cat {temp}/scratchpad/f >&3; sh x.sh`,
		`{ cat {temp}/scratchpad/f >&3; } 3>x.sh`,
	} {
		command = f.expand(command)
		a := Action{Kind: "exec", Target: command, Role: "implement", Work: f.work, Cwd: f.work, State: f.work, Temp: []string{f.temp}}
		if d := Evaluate(p, LeadRules{}, mode.Full, a); d.Kind == Allow || !PersonOnly(d) {
			t.Errorf("%q in full: %+v", command, d)
		}
	}
}
