package policy

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/core/mode"
)

func quotedDataEval(t *testing.T, command string, readOnly bool) Decision {
	t.Helper()
	if readOnly {
		return Evaluate(policy(t), LeadRules{RoleSandbox: "read-only"}, mode.Full, Action{Kind: "exec", Target: command, Role: "review_final"})
	}
	return Evaluate(policy(t), LeadRules{}, mode.Edits, Action{Kind: "exec", Target: command, Role: "implement"})
}

// The search pattern of rg, quoted, is data: it gets the decision of the same
// rg with a plain pattern, for write roles and read-only roles alike.
func TestQuotedRgPatternIsData(t *testing.T) {
	for _, tc := range []struct{ command, plain, want string }{
		// The glob is unquoted, so this one asks as a path glob, as elsewhere.
		{`rg -n 'no live lead|StateRoot|Select' internal/adapter/session cmd/duolog/lead* internal/app/peer/env.go`,
			`rg -n plain internal/adapter/session cmd/duolog/lead* internal/app/peer/env.go`, Ask},
		{`rg -n -F 'func (r *Registry)' internal/session/session.go`, `rg -n -F plain internal/session/session.go`, Allow},
		{`rg -n "StateRoot\(|Root" --type go`, `rg -n plain --type go`, Allow},
		{`rg -n 'a*b' internal`, `rg -n plain internal`, Allow},
		{`rg -n "a*b" internal`, `rg -n plain internal`, Allow},
		{`rg -n "a *" internal`, `rg -n plain internal`, Allow},
		{`rg -n 'a|b;c&d$e(f)*' internal`, `rg -n plain internal`, Allow},
		{`rg -n "a|b;c&d(e)*" internal`, `rg -n plain internal`, Allow},
		{`rg -n "a\|b\;c\&d\(e\)\*" internal`, `rg -n plain internal`, Allow},
		{`rg -- 'a*' internal`, `rg -- plain internal`, Allow},
		{`rg -n 'a*b' internal | head -5`, `rg -n plain internal | head -5`, Allow},
	} {
		for _, readOnly := range []bool{false, true} {
			got, plain := quotedDataEval(t, tc.command, readOnly), quotedDataEval(t, tc.plain, readOnly)
			if got.Kind != tc.want || plain.Kind != tc.want {
				t.Errorf("read-only %v %q: %s (%s), plain %s (%s), want %s", readOnly, tc.command, got.Kind, got.Reason, plain.Kind, plain.Reason, tc.want)
			}
		}
		// The same in plan mode, where only a read proven certain runs.
		lead := LeadRules{}
		got := Evaluate(policy(t), lead, mode.Plan, Action{Kind: "exec", Target: tc.command, Role: "implement"})
		plain := Evaluate(policy(t), lead, mode.Plan, Action{Kind: "exec", Target: tc.plain, Role: "implement"})
		if got.Kind != plain.Kind {
			t.Errorf("plan %q: %s (%s), plain %s (%s)", tc.command, got.Kind, got.Reason, plain.Kind, plain.Reason)
		}
	}
}

// The lead guard reads ShellParts, which keeps treating such text as uncertain.
func TestShellPartsStaysConservativeForQuotedPatterns(t *testing.T) {
	for _, command := range []string{`rg -n 'a*b' internal`, `rg -n "StateRoot\(|Root" --type go`} {
		if _, uncertain := ShellParts(command, ""); !uncertain {
			t.Errorf("ShellParts %q is certain", command)
		}
	}
}

// What the quoted-pattern reading must leave as it was: unquoted operators,
// substitutions, options that run a program, denied paths, and quoted text
// outside the search pattern.
func TestQuotedRgStaysRefused(t *testing.T) {
	for _, tc := range []struct{ command, writer, readOnly string }{
		// Unquoted operators split the command.
		{`rg x | sh`, Ask, Deny},
		{`rg x; rm y`, Ask, Deny},
		{`rg x && curl y`, Ask, Deny},
		{`rg 'a*' src | sh`, Ask, Deny},
		{`rg 'a*' src; rm y`, Ask, Deny},
		{`rg 'a*' src && curl y`, Ask, Deny},
		// A substitution runs inside double quotes.
		{`rg "$(cmd)" .`, Ask, Deny},
		{"rg \"`cmd`\" .", Ask, Deny},
		{`rg "a*$(cmd)" .`, Ask, Deny},
		{"rg \"a*`cmd`\" .", Ask, Deny},
		{`rg "$x*" .`, Ask, Deny},
		// A variable or a substitution outside quotes is no quoted text.
		{`rg 'a*' $x`, Ask, Deny},
		{`rg 'a*' $(cmd)`, Ask, Deny},
		// An option that runs a program, quoted or not.
		{`rg --pre cmd x`, Ask, Deny},
		{`rg 'a*' --pre cmd x`, Ask, Deny},
		{`rg '--pre=cmd*' x`, Ask, Deny},
		{`rg "--pre=c(m)d" x`, Ask, Deny},
		// A denied path, as an operand or named in the pattern.
		{`rg x .duolog/bus.ndjson`, Deny, Deny},
		{`rg 'a*' .duolog/bus.ndjson`, Deny, Deny},
		{`rg "a b*" .duolog/bus.ndjson`, Deny, Deny},
		{`rg x .env`, Deny, Deny},
		{`rg 'a*' .env`, Deny, Deny},
		{`rg 'a b*' .env`, Deny, Deny},
		{`rg "a\(" .env`, Deny, Deny},
		{`rg 'a*' secrets/prod.key`, Deny, Deny},
		{`rg '.env*' src`, Ask, Deny},
		{`rg '.duolog*' src`, Ask, Deny},
		{`rg 'secrets/*' src`, Ask, Deny},
		{`rg '*.pem' src`, Ask, Deny},
		{`rg "*.key" src`, Ask, Deny},
		// Quoted text outside the search pattern reads as before.
		{`rg x 'a*'`, Ask, Deny},
		{`rg 'a*' 'b*'`, Ask, Deny},
		{`rg -e 'a*' src`, Ask, Deny},
		{`rg -e "a\(" src`, Ask, Deny},
		{`rg --regexp='a*' src`, Ask, Deny},
		{`rg x -r 'a*' src`, Ask, Deny},
		{`rg --files 'a*'`, Ask, Deny},
		// What the list does not name stays unverified.
		{`rg '' src`, Ask, Deny},
		{`rg 'a?b' src`, Ask, Deny},
		{`rg 'a[b]c*' src`, Ask, Deny},
		{`rg 'a{b}*' src`, Ask, Deny},
		{`rg "a\bc" src`, Ask, Deny},
		{`rg 'a\(b' src`, Ask, Deny},
		{`rg "a\$b" src`, Ask, Deny},
		{`rg "a\"b*" src`, Ask, Deny},
		{`rg "a\\(b" src`, Ask, Deny},
		{"rg 'a\tb*' src", Ask, Deny},
		// Other programs keep their reading.
		{`grep 'a*' src/a.go`, Ask, Deny},
		{`grep "a\(" src/a.go`, Ask, Deny},
		{`cat 'a*'`, Ask, Deny},
		{`find . -name 'a*' -delete`, Ask, Deny},
	} {
		if got := quotedDataEval(t, tc.command, false); got.Kind != tc.writer {
			t.Errorf("writer %q: %s (%s), want %s", tc.command, got.Kind, got.Reason, tc.writer)
		}
		if got := quotedDataEval(t, tc.command, true); got.Kind != tc.readOnly {
			t.Errorf("read-only %q: %s (%s), want %s", tc.command, got.Kind, got.Reason, tc.readOnly)
		}
	}
}

// A test name given to go test -run or -skip is no command, even when it holds
// the name of an agent.
func TestGoTestNameValueIsNotACommand(t *testing.T) {
	for _, command := range []string{
		`go test ./internal/install/... -run TestHooksJSONGolden/claude -update`,
		`go test -run TestHooksJSONGolden/codex ./internal/install/...`,
		`go test -skip TestHooksJSONGolden/claude ./internal/install/...`,
		`go test -run=TestHooksJSONGolden/claude ./internal/install/...`,
		`go test -skip=TestHooksJSONGolden/codex -update ./internal/install/...`,
		`go test ./... -run claude -v`,
	} {
		if got := quotedDataEval(t, command, false); got.Kind != Allow {
			t.Errorf("writer %q: %s (%s, %s), want allow", command, got.Kind, got.Reason, got.Rule)
		}
		// A read-only role runs no test at all.
		if got := quotedDataEval(t, command, true); got.Kind != Deny {
			t.Errorf("read-only %q: %s (%s), want deny", command, got.Kind, got.Reason)
		}
	}
}

func TestGoTestAgentStaysRefused(t *testing.T) {
	for _, tc := range []struct{ command, writer, readOnly string }{
		{`go test -exec claude ./...`, Deny, Deny},
		{`go test -run x -exec claude ./...`, Deny, Deny},
		{`go test -toolexec claude -run x ./...`, Deny, Deny},
		// A program that runs the tests sees the test name as part of its run.
		{`go test -exec sh -run TestX/claude ./...`, Deny, Deny},
		{`go test -run TestX/claude -toolexec sh ./...`, Deny, Deny},
		{`go test -vettool=x -run TestX/claude ./...`, Deny, Deny},
		{`go test --exec sh -run TestX/claude ./...`, Deny, Deny},
		{`go test --toolexec=sh -run TestX/claude ./...`, Deny, Deny},
		// A separator ends the value.
		{`go test -run x ./... ; claude -p y`, Deny, Deny},
		{`go test -run x ./... && codex exec y`, Deny, Deny},
		{`go test -run claude ./... ; claude -p y`, Deny, Deny},
		// A substitution in the value runs.
		{`go test -run "$(cmd)" ./...`, Ask, Deny},
		{`go test -run "$(claude -p y)" ./...`, Deny, Deny},
		{`go test -run=$(claude) ./...`, Deny, Deny},
		{"go test -run `claude -p y` ./...", Deny, Deny},
		// Only the one value after -run or -skip is a test name.
		{`go test -run x claude -p y`, Deny, Deny},
		{`go vet -run TestX/claude ./...`, Deny, Deny},
		{`go build -run TestX/claude ./...`, Deny, Deny},
		{`go run -run TestX/claude ./...`, Deny, Deny},
		{`go -run TestX/claude ./...`, Deny, Deny},
		{`make -run TestX/claude -update`, Deny, Deny},
		// Only the agent rules skip the value; another deny still holds.
		{`go test -run git push ./...`, Deny, Deny},
	} {
		if got := quotedDataEval(t, tc.command, false); got.Kind != tc.writer {
			t.Errorf("writer %q: %s (%s), want %s", tc.command, got.Kind, got.Reason, tc.writer)
		}
		if got := quotedDataEval(t, tc.command, true); got.Kind != tc.readOnly {
			t.Errorf("read-only %q: %s (%s), want %s", tc.command, got.Kind, got.Reason, tc.readOnly)
		}
	}
}

// Two Git commands that print one name are reads, word for word.
func TestExactGitReads(t *testing.T) {
	p := policy(t)
	for _, command := range []string{`git branch --show-current`, `git rev-parse --abbrev-ref HEAD`, `git rev-parse`, `git rev-parse HEAD`,
		`git rev-parse --short HEAD`, `git rev-parse --show-toplevel`, `git rev-parse --is-inside-work-tree`} {
		for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Edits, mode.Full} {
			if got := Evaluate(p, LeadRules{}, m, Action{Kind: "exec", Target: command, Role: "implement"}); got.Kind != Allow {
				t.Errorf("writer %s %q: %s (%s), want allow", m, command, got.Kind, got.Reason)
			}
			if got := Evaluate(p, LeadRules{RoleSandbox: "read-only"}, m, Action{Kind: "exec", Target: command, Role: "review_final"}); got.Kind != Allow {
				t.Errorf("read-only %s %q: %s (%s), want allow", m, command, got.Kind, got.Reason)
			}
		}
	}
}

func TestOtherGitBranchAndRevParseStayRefused(t *testing.T) {
	for _, command := range []string{
		`git branch`,
		`git branch -a`,
		`git branch -D x`,
		`git branch new`,
		`git branch --show-current x`,
		`git branch --show-currentx`,
		`git branch -D --show-current`,
		`git rev-parse HEAD x`,
		`git rev-parse --git-path x`,
		`git rev-parse --abbrev-ref HEAD x`,
		`git rev-parse --abbrev-ref main`,
		`git rev-parse --abbrev-ref head`,
		`git rev-parse --abbrev-ref HEAD --git-path x`,
		`git -C x branch --show-current`,
		`git -c core.pager=x branch --show-current`,
		`git branch --show-current; rm y`,
		`ps`,
		`ps aux`,
		`ps -ef`,
	} {
		if got := quotedDataEval(t, command, false); got.Kind != Ask {
			t.Errorf("writer %q: %s (%s), want ask", command, got.Kind, got.Reason)
		}
		if got := quotedDataEval(t, command, true); got.Kind != Deny {
			t.Errorf("read-only %q: %s (%s), want deny", command, got.Kind, got.Reason)
		}
	}
}
