package policy

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/core/mode"
)

// The Go toolchain run through $(go env GOROOT)/bin/go, the way around a
// version manager's shim, follows the rules for go.
func TestGoRootToolchainFollowsGoRules(t *testing.T) {
	p := policy(t)
	work := t.TempDir()
	exec := func(command string) Action {
		return Action{Kind: "exec", Target: command, Role: "implement", Work: work, Cwd: work, State: work}
	}
	for _, command := range []string{
		`$(go env GOROOT)/bin/go test ./internal/app/peer/ -run 'TestPeer(Close|Open)'`,
		`$(go env GOROOT)/bin/go vet ./...`,
		`"$(go env GOROOT)/bin/go" test ./...`,
		`"$(go env GOROOT)"/bin/go build -o .cache/bin/duolog ./cmd/duolog`,
		`$(go env GOROOT)/bin/gofmt -l internal`,
		`$(go env GOROOT)/bin/go test ./... | tail -20`,
		`$(go env GOROOT)/bin/go vet ./... && $(go env GOROOT)/bin/go test ./...`,
		"$(go env GOROOT)/bin/go vet ./...\n$(go env GOROOT)/bin/go test ./...",
	} {
		check(t, p, LeadRules{}, mode.Edits, exec(command), Allow)
	}
	// go run asks, so the same run through GOROOT asks too.
	check(t, p, LeadRules{}, mode.Edits, exec(`$(go env GOROOT)/bin/go run ./cmd/x`), Ask)
	for _, command := range []string{
		`$(go env GOROOT)/bin/go build -o .git/hooks/pre-commit ./x`,
		`$(go env GOROOT)/bin/go test ./... && git push origin main`,
	} {
		check(t, p, LeadRules{}, mode.Full, exec(command), Deny)
	}
}

// Only the exact form names the toolchain. Any other program from a
// substitution, another variable or a GOROOT the line itself changes still
// asks, in full mode too.
func TestOtherSubstitutedProgramsStillAsk(t *testing.T) {
	p := policy(t)
	work := t.TempDir()
	for _, command := range []string{
		`$(go env GOPATH)/bin/go test ./...`,
		`$(go env GOROOT)/bin/gox test ./...`,
		`$(go env GOROOT)/bin/go/../../x test ./...`,
		`$(go env GOROOT)/../evil test ./...`,
		`$(go env -w GOROOT=/tmp)/bin/go test ./...`,
		`$(go  env GOROOT)/bin/go test ./...`,
		`x$(go env GOROOT)/bin/go test ./...`,
		`GOROOT=/tmp/evil $(go env GOROOT)/bin/go test ./...`,
		`export GOROOT=/tmp/evil; $(go env GOROOT)/bin/go test ./...`,
		`GOTOOLCHAIN=local $(go env GOROOT)/bin/go test ./...`,
		`$(go env GOROOT)/bin/go test ./... $(rm -rf x)`,
		"$(go env GOROOT)/bin/go test `id`",
		`IFS=/; $(go env GOROOT)/bin/go test ./...`,
		`go() { echo /tmp/evil; }; $(go env GOROOT)/bin/go test ./...`,
		`alias go=/tmp/evil; $(go env GOROOT)/bin/go test ./...`,
		`GO=$(go env GOROOT)/bin/go; $GO vet ./... ; $GO test ./internal/app/peer/...`,
		`GO="$(go env GOROOT)/bin/go" && "$GO" test ./... && ${GO} vet ./...`,
		`GO=$(go env GOROOT)/bin/go; GO=/tmp/evil; $GO test ./...`,
		`GO=$(go env GOROOT)/bin/go; read GO; $GO test ./...`,
		`GO=$(go env GOROOT)/bin/go; $GO test ./...; echo $GO`,
		`GO=$(go env GOROOT)/bin/go $GO test ./...`,
		`PATH=$(go env GOROOT)/bin/go; $PATH test ./...`,
		`ls; GO=$(go env GOROOT)/bin/go; $GO test ./...`,
		`$GO test ./...`,
		`echo '$(go env GOROOT)/bin/go test'; $(go env GOROOT)/bin/go run ./x`,
	} {
		for _, m := range []mode.Mode{mode.Edits, mode.Full} {
			d := Evaluate(p, LeadRules{}, m, Action{Kind: "exec", Target: command, Role: "implement", Work: work, Cwd: work, State: work})
			if d.Kind == Allow {
				t.Errorf("%q in %s: %+v, want no allow", command, m, d)
			}
		}
	}
}

// A here-document that only cat into /dev/null or : reads runs nothing, so
// the commands around it are judged as if it were not there.
func TestInertHeredocIsSkipped(t *testing.T) {
	p := policy(t)
	work := t.TempDir()
	exec := func(command string) Action {
		return Action{Kind: "exec", Target: command, Role: "implement", Work: work, Cwd: work, State: work}
	}
	for _, command := range []string{
		"cat > /dev/null <<'EOF'\nnote: check the gate next\nEOF\ncat go.mod",
		"cat go.mod\ncat >/dev/null <<'EOF'\nEOF",
		"cat > /dev/null <<'EOF'\nfirst\nEOF\nsed -n 1,20p go.mod\ncat > /dev/null <<'EOF'\nafter\nEOF",
		"cat <<\"EOF\" > /dev/null\n$(rm x) is only text here\nEOF\ngo test ./...",
		"cat 1>/dev/null <<-EOF\n\tplain words\n\tEOF\nls",
		": <<'EOF'\nanything at all; git status\nEOF\ngo vet ./...",
		"cat >/dev/null <<'EOF'\nnote\nEOF\n$(go env GOROOT)/bin/go test ./...",
	} {
		check(t, p, LeadRules{}, mode.Edits, exec(command), Allow)
	}
	// What develop denies stays denied, whatever the rewrite would see.
	for _, command := range []string{
		"cat > /dev/null <<'EOF'\nhello $(rm -rf /)\nEOF",
		"cat > /dev/null <<'EOF'\nnote\nEOF\ngit push origin main",
	} {
		check(t, p, LeadRules{}, mode.Full, exec(command), Deny)
	}
}

// A body that something runs, expands or writes is not inert, and neither is
// one that a pipe or a list continues.
func TestPlainExecLeavesOtherHeredocs(t *testing.T) {
	for _, command := range []string{
		"cat <<'EOF' | sh\nrm -rf x\nEOF",
		"python3 - <<'EOF'\nprint(1)\nEOF",
		"cat > out.txt <<'EOF'\nx\nEOF\nls",
		"cat <<'EOF'\nx\nEOF\nls",
		"cat 2>/dev/null <<'EOF'\nx\nEOF\nls",
		"cat go.mod > /dev/null <<'EOF'\nx\nEOF\nls",
		"tee >/dev/null <<'EOF'\nx\nEOF\nls",
		"cat > /dev/null <<EOF\n$(touch x)\nEOF\nls",
		"cat > /dev/null <<EOF\n${X:=y}\nEOF\nls",
		"cat > /dev/null <<'EOF' && rm -rf x\nbody\nEOF\nls",
		"cat > /dev/null <<'A' <<'B'\na\nA\nb\nB\nls",
		"cat > /dev/null <<'EOF'\nnever closed",
		"ls |\ncat > /dev/null <<'EOF'\nx\nEOF\nls",
		"ls &&\ncat > /dev/null <<'EOF'\nx\nEOF\nls",
		"echo 'x\ncat > /dev/null <<'EOF'\ny\nEOF\n'",
		"cat > /dev/null <<'EOF'\nnote\nEOF",
		"ls # '\ncat > /dev/null <<'EOF'\nx\nEOF\nls",
		// A line that can make cat or : run something else keeps the body.
		"alias cat=reboot\ncat > /dev/null <<'EOF'\nx\nEOF\nls",
		"cat() { reboot; }\ncat > /dev/null <<'EOF'\nx\nEOF\nls",
		":(){ reboot; }\n: <<'EOF'\nx\nEOF\nls",
		"function cat { reboot; }\ncat > /dev/null <<'EOF'\nx\nEOF\nls",
		"trap reboot DEBUG\n: <<'EOF'\nx\nEOF\nls",
		"hash -p /tmp/x cat\ncat > /dev/null <<'EOF'\nx\nEOF\nls",
	} {
		if got, ok := plainExec(command); ok {
			t.Errorf("%q became %q, want it left as written", command, got)
		}
	}
}

func TestPlainExecRewrites(t *testing.T) {
	for command, want := range map[string]string{
		`$(go env GOROOT)/bin/go test ./...`:                          `go test ./...`,
		`"$(go env GOROOT)"/bin/gofmt -l . | head`:                    `gofmt -l . | head`,
		`ls 2>&1 | $(go env GOROOT)/bin/go vet`:                       `ls 2>&1 | go vet`,
		`ls |& $(go env GOROOT)/bin/go vet`:                           `ls |& go vet`,
		"cat > /dev/null <<'EOF'\nnote\nEOF\ncat go.mod":              "cat go.mod",
		"ls\n: <<EOF\nnote\nEOF\nls":                                  "ls\nls",
		`echo "$(go env GOROOT)/bin/go"; $(go env GOROOT)/bin/go vet`: ``,
		// The variable form is not read: a quoted name can set it again.
		`GO=$(go env GOROOT)/bin/go; $GO vet ./... ; $GO test ./...`: ``,
		`GO=$(go env GOROOT)/bin/go; export "G""O=git"; $GO push`:    ``,
		// After >|, >& or <& the word is a file, not a program.
		`echo hi >| $(go env GOROOT)/bin/go`:       ``,
		`tee >& "$(go env GOROOT)/bin/go" < x`:     ``,
		`cat <& $(go env GOROOT)/bin/go`:           ``,
		`echo hi > $(go env GOROOT)/bin/go`:        ``,
		`echo hi >>$(go env GOROOT)/bin/go`:        ``,
		`cd $(go env GOROOT)/bin/go`:               ``,
		`go() { :; }; $(go env GOROOT)/bin/go vet`: ``,
	} {
		got, ok := plainExec(command)
		if want == "" {
			if ok {
				t.Errorf("%q became %q, want it left as written", command, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("%q became %q, %v; want %q", command, got, ok, want)
		}
	}
}

// PowerShell and cmd.exe read neither form, so nothing is rewritten there.
func TestPlainExecIsPosixOnly(t *testing.T) {
	p := policy(t)
	for _, shell := range []string{"powershell", "windows"} {
		d := Evaluate(p, LeadRules{}, mode.Edits, Action{Kind: "exec", Target: `$(go env GOROOT)/bin/go test ./...`, Role: "implement", Shell: shell})
		if d.Kind == Allow {
			t.Errorf("%s: %+v", shell, d)
		}
	}
}

// reportCase is one command of the security review, with develop's decision
// in edits and full mode first and then this branch's.
type reportCase struct {
	command                   string
	developEdits, developFull string
	edits, full               string
}

// reportCases are the commands of the security review of the GOROOT and
// here-document reading. develop is the decision before it: what Evaluate
// decides for the line as written, before the go env -w ask.
var reportCases = []reportCase{
	// 1. A variable that holds the toolchain is set again under a quoted name.
	{`GO=$(go env GOROOT)/bin/go; export "G""O=git"; $GO push origin main`, Ask, Ask, Ask, Ask},
	{`GO=$(go env GOROOT)/bin/go; export "G""O=claude"; $GO -p hi`, Ask, Ask, Ask, Ask},
	{`GO=$(go env GOROOT)/bin/go; readonly "G"O=./evil; $GO test ./...`, Ask, Ask, Ask, Ask},
	{`GO=$(go env GOROOT)/bin/go; export "G""O=rm"; $GO -rf .`, Ask, Ask, Ask, Ask},
	{`GO=$(go env GOROOT)/bin/go; export "G""O=sh"; $GO vet`, Ask, Ask, Ask, Ask},
	{`GO=$(go env GOROOT)/bin/go; export G''O=git; $GO push origin main`, Ask, Ask, Ask, Ask},
	{`GO=$(go env GOROOT)/bin/go; export G\O=git; $GO push origin main`, Ask, Ask, Ask, Ask},
	{`GO=$(go env GOROOT)/bin/go; printf -v G''O git; $GO push origin main`, Ask, Ask, Ask, Ask},
	{`GO=$(go env GOROOT)/bin/go; read G''O; $GO push origin main`, Ask, Ask, Ask, Ask},
	{`GO=$(go env GOROOT)/bin/go; declare G''O=git; $GO push origin main`, Ask, Ask, Ask, Ask},
	{`GO=$(go env GOROOT)/bin/go; eval 'GO=git'; $GO push origin main`, Ask, Ask, Ask, Ask},
	{`GO=$(go env GOROOT)/bin/go; $GO vet ./... ; $GO test ./internal/app/peer/...`, Ask, Ask, Ask, Ask},
	{`GO="$(go env GOROOT)/bin/go" && "$GO" test ./... && ${GO} vet ./...`, Ask, Ask, Ask, Ask},
	{"GO=$(go env GOROOT)/bin/go\n$GO test ./...", Ask, Ask, Ask, Ask},
	{`GO=$(go env GOROOT)/bin/go $GO test ./...`, Ask, Ask, Ask, Ask},
	{`ls; GO=$(go env GOROOT)/bin/go; $GO test ./...`, Ask, Ask, Ask, Ask},
	{`GO=/x; $GO test ./...`, Ask, Ask, Ask, Ask},
	{`GO=$(go env GOROOT)/bin/go; read GO; $GO test ./...`, Ask, Ask, Ask, Ask},
	{`GO=$(go env GOROOT)/bin/go; $GO test ./...; echo $GO`, Ask, Ask, Ask, Ask},
	// 2. go env -w and -u write the user's Go env file outside the work root.
	{`$(go env GOROOT)/bin/go env -w GOFLAGS=-toolexec=./evil`, Ask, Ask, Ask, Ask},
	{`go env -w GOFLAGS=-toolexec=./evil`, Ask, Allow, Ask, Ask},
	{`go env -u GOPROXY`, Ask, Allow, Ask, Ask},
	{`go env --w=GOFLAGS=-x`, Ask, Allow, Ask, Ask},
	{`go -C . env -w CC=./evil`, Ask, Allow, Ask, Ask},
	{`cat go.mod; go env -w GOPROXY=http://x`, Ask, Allow, Ask, Ask},
	{`env go env -w GOPROXY=http://x`, Ask, Ask, Ask, Ask},
	{`go env GOPATH`, Ask, Allow, Ask, Allow},
	// 3. A redirect target is a file, not a program.
	{`echo hi >| $(go env GOROOT)/bin/go`, Ask, Ask, Ask, Ask},
	{`tee >& "$(go env GOROOT)/bin/go" < x`, Ask, Ask, Ask, Ask},
	{`GO=$(go env GOROOT)/bin/go; cp evil >| $GO`, Ask, Ask, Ask, Ask},
	// Settings that change the folder go env GOROOT names, or redefine go.
	{`GOROOT=/tmp/evil $(go env GOROOT)/bin/go test ./...`, Ask, Ask, Ask, Ask},
	{`export GOROOT=/tmp/evil; $(go env GOROOT)/bin/go test ./...`, Ask, Ask, Ask, Ask},
	{`GOENV=./x $(go env GOROOT)/bin/go test ./...`, Ask, Ask, Ask, Ask},
	{`export GOTOOLCHAIN=go1.99; $(go env GOROOT)/bin/go test ./...`, Ask, Ask, Ask, Ask},
	{`IFS=/; $(go env GOROOT)/bin/go test ./...`, Ask, Ask, Ask, Ask},
	{`go() { echo /tmp/evil; }; $(go env GOROOT)/bin/go test ./...`, Ask, Ask, Ask, Ask},
	{`alias go=/tmp/evil; $(go env GOROOT)/bin/go test ./...`, Ask, Ask, Ask, Ask},
	{`PATH=./bin:$PATH; $(go env GOROOT)/bin/go test ./...`, Ask, Ask, Ask, Ask},
	{`GOFLAGS=-toolexec=./evil; $(go env GOROOT)/bin/go test ./...`, Ask, Ask, Ask, Ask},
	{`cd /tmp && $(go env GOROOT)/bin/go test ./...`, Ask, Ask, Ask, Ask},
	// Prefixes and other substitutions.
	{`env $(go env GOROOT)/bin/go test ./...`, Ask, Ask, Ask, Ask},
	{`command $(go env GOROOT)/bin/go test ./...`, Ask, Ask, Ask, Ask},
	{`x$(go env GOROOT)/bin/go test ./...`, Ask, Ask, Ask, Ask},
	{`$(go env GOPATH)/bin/go test ./...`, Ask, Ask, Ask, Ask},
	{"`go env GOROOT`/bin/go test ./...", Ask, Ask, Ask, Ask},
	// Here-documents.
	{"cat > /dev/null <<'EOF'\nhello $(rm -rf ~)\nEOF", Deny, Deny, Deny, Deny},
	{"cat > /dev/null <<EOF\n$HOME\nEOF\nls", Ask, Ask, Ask, Ask},
	{"cat > /dev/null <<EOF\n`id`\nEOF\nls", Ask, Ask, Ask, Ask},
	{"cat > /dev/null <<EOF\na\\\nEOF\nls", Ask, Ask, Ask, Ask},
	{"cat >/dev/null <<'EOF'\nEOF\ngit push origin main\nEOF", Deny, Deny, Deny, Deny},
	{"cat >/dev/null <<-'EOF'\n\tnote\n\tEOF\nls", Ask, Ask, Allow, Allow},
	{"cat >/dev/null <<'EOF'\r\nnote\r\nEOF\r\nls", Ask, Ask, Allow, Allow},
	{"cat >/dev/null <<< 'note'\nls", Ask, Ask, Ask, Ask},
	{"ls |\ncat > /dev/null <<'EOF'\nx\nEOF\nls", Ask, Ask, Ask, Ask},
	{"ls \\\ncat > /dev/null <<'EOF'\nx\nEOF\nls", Ask, Ask, Ask, Ask},
	{"cat > /dev/null <<'A'\ncat > /dev/null <<'B'\nA\nls", Ask, Ask, Allow, Allow},
	{"cat(){ reboot; }\ncat > /dev/null <<'EOF'\nx\nEOF\nls", Ask, Ask, Ask, Ask},
	{"cat (){ reboot; }\ncat > /dev/null <<'EOF'\nx\nEOF\nls", Ask, Ask, Ask, Ask},
	{"cat\t(){ reboot; }\ncat > /dev/null <<'EOF'\nx\nEOF\nls", Ask, Ask, Ask, Ask},
	{"alias cat=reboot\ncat > /dev/null <<'EOF'\nx\nEOF\nls", Ask, Ask, Ask, Ask},
	{"trap reboot DEBUG\n: <<'EOF'\nx\nEOF\nls", Ask, Ask, Ask, Ask},
	{"hash -p /tmp/x cat\ncat > /dev/null <<'EOF'\nx\nEOF\nls", Ask, Ask, Ask, Ask},
	{"enable -n cat\ncat > /dev/null <<'EOF'\nx\nEOF\nls", Ask, Ask, Ask, Ask},
	{"command cat > /dev/null <<'EOF'\nx\nEOF\nls", Ask, Ask, Ask, Ask},
	{"set -o noclobber\ncat > /dev/null <<'EOF'\nx\nEOF\nls", Ask, Ask, Ask, Allow},
	{"exec >out\ncat > /dev/null <<'EOF'\nx\nEOF\nls", Ask, Ask, Ask, Ask},
	{"cat >/dev/null <<'EOF'\nx\nEOF\n| sh x.sh", Ask, Ask, Ask, Allow},
	{"python3 - <<'EOF'\nprint(1)\nEOF", Ask, Ask, Ask, Ask},
	// The forms of the task, which now need no ask.
	{`$(go env GOROOT)/bin/go test ./internal/app/peer/ -run 'TestPeer(Close|Open)'`, Ask, Ask, Allow, Allow},
	{"cat > /dev/null <<'EOF'\nnote\nEOF\ncat go.mod", Ask, Ask, Allow, Allow},
	{`uv run scripts/lint/exceptions.py update`, Ask, Allow, Ask, Allow},
}

// Every command of the security review keeps develop's decision, except that
// the plain $(go env GOROOT)/bin/go and the inert here-document forms may now
// be allowed, and go env -w and -u ask. Nothing develop denies is allowed.
func TestSecurityReviewCommands(t *testing.T) {
	p := policy(t)
	work := t.TempDir()
	for _, c := range reportCases {
		for _, m := range []struct {
			mode          mode.Mode
			develop, want string
		}{{mode.Edits, c.developEdits, c.edits}, {mode.Full, c.developFull, c.full}} {
			a := Action{Kind: "exec", Target: c.command, Role: "implement", Work: work, Cwd: work, State: work}
			if got := Evaluate(p, LeadRules{}, m.mode, a); got.Kind != m.want {
				t.Errorf("%q in %s: %+v, want %s", c.command, m.mode, got, m.want)
			}
			// The line as written is develop's reading, but for go env -w.
			a.asWritten = true
			if got := Evaluate(p, LeadRules{}, m.mode, a); !goEnvWrites(c.command) && got.Kind != m.develop {
				t.Errorf("%q in %s as written: %+v, but develop is %s", c.command, m.mode, got, m.develop)
			}
			if _, plain := plainExec(c.command); m.develop == Deny && m.want != Deny || m.want == Allow && m.develop != Allow && !plain {
				t.Errorf("%q in %s: develop %s, now %s", c.command, m.mode, m.develop, m.want)
			}
		}
	}
}

// go env -w and -u ask the person in every mode: they change the user's Go
// env file, which every later go run reads. Reading the env does not.
func TestGoEnvWritesAskThePerson(t *testing.T) {
	p := policy(t)
	work := t.TempDir()
	for _, command := range []string{
		`go env -w GOFLAGS=-toolexec=./evil`,
		`go env -u GOPROXY`,
		`go env -w=true GOPROXY=x`,
		`go env --u GOPROXY`,
		`"go" env "-w" CC=./evil`,
		`/usr/local/go/bin/go env -w GOPROXY=x`,
		`go -C sub env -w GOPROXY=x`,
		`go -C=sub env -w GOPROXY=x`,
		`ls && go env -w GOPROXY=x`,
	} {
		for _, m := range []mode.Mode{mode.Edits, mode.Full} {
			d := Evaluate(p, LeadRules{}, m, Action{Kind: "exec", Target: command, Role: "implement", Work: work, Cwd: work, State: work})
			if d.Kind != Ask || !PersonOnly(d) {
				t.Errorf("%q in %s: %+v, want an ask of the person", command, m, d)
			}
		}
	}
	for _, command := range []string{`go env GOPATH`, `go env -json`, `go env -changed`, `go vet -w ./...`, `go test ./... -run Env`} {
		if goEnvWrites(command) {
			t.Errorf("%q reads as go env -w", command)
		}
	}
}
