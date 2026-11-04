package gate

import (
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
)

// Quotes that only group literal text read as literals, so the usual quoted
// search patterns are plain reads. Anything a shell could split, expand or run
// differently from the quote-free text stays a question, for writers and for
// read-only roles alike.
func TestLiteralQuotesAndTheirBypasses(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct {
		command  string
		writer   string // implement in edits mode
		readOnly string // review_final in full mode
	}{
		{`grep -n "PROJ-1" CHANGES.md`, Allow, Allow},
		{`rg -n "useState" src`, Allow, Allow},
		{`grep -E "a|b" src/a.go`, Allow, Allow},
		{`git log --format="%h" -5`, Allow, Allow},
		{`go test -run 'a|b' ./...`, Allow, Deny},
		// A newline inside quotes is a second sed command; sed can write and run.
		{"sed -n '1p\nw .git/hooks/pre-commit' README.md", Ask, Deny},
		{"sed -n '1p\ne touch pwned' README.md", Ask, Deny},
		// An empty quoted word would vanish and turn the file into the pattern.
		{`grep '' .env`, Ask, Deny},
		{`grep "" README.md`, Ask, Deny},
		{`rg '' secrets/prod.key`, Ask, Deny},
		{`grep -- '' README.md`, Ask, Deny},
		// A quoted word with spaces is one argument of a read command; globs inside quotes are not literal words.
		{`grep "a b" README.md`, Allow, Allow},
		{`go test -run 'a b' ./...`, Ask, Deny},
		// The quoted -g stays in the pattern; the glob operand still asks.
		{`rg '^|a -g' .e*`, Ask, Ask},
		{`rg --files src -g '*test*'`, Allow, Allow},
		// A glob or brace in a separate option value expands onto other files.
		{`grep ^ -A {1,.env}`, Ask, Deny},
		{`rg ^ -g {x,.env}`, Ask, Deny},
		{`rg ^ -A {1,~/.ssh/id_rsa}`, Ask, Deny},
		{`grep -vf secrets/* src/a.go`, Ask, Ask},
		{`rg ^ -g .e*`, Ask, Ask},
		{`grep -f{x,.env} src/a.go`, Ask, Deny},
		// grep's --context and -C take an attached optional value; the next word is the pattern.
		{`grep --context . .env`, Deny, Deny},
		{`grep -C . .env`, Deny, Deny},
		// A value of -e that looks like -- must not end option checking.
		{`rg -e '--' --pre=./payload README.md`, Ask, Deny},
		{`rg -e -- --pre=./payload README.md`, Ask, Deny},
		// Braces expand anywhere, also in a tool's output option.
		{`npx --no eslint --output-file={out,.duolog/config.yaml} src/a.js`, Ask, Deny},
		// Substitutions stay unverified inside double quotes.
		{`grep "$(rm -rf x)" src/a.go`, Ask, Deny},
		{"grep \"`id`\" src/a.go", Ask, Deny},
	} {
		got := Evaluate(p, LeadRules{}, mode.Edits, Action{Kind: "exec", Target: tc.command, Role: "implement"})
		if got.Kind != tc.writer {
			t.Errorf("writer %q: %s (%s), want %s", tc.command, got.Kind, got.Reason, tc.writer)
		}
		got = Evaluate(p, LeadRules{RoleSandbox: "read-only"}, mode.Full, Action{Kind: "exec", Target: tc.command, Role: "review_final"})
		if got.Kind != tc.readOnly {
			t.Errorf("read-only %q: %s (%s), want %s", tc.command, got.Kind, got.Reason, tc.readOnly)
		}
	}
}

// A quoted word with spaces stays one argument of a read command, and a read
// command may drop its output into /dev/null. Other redirects still write.
func TestQuotedSpacesAndDevNullRedirectsInReads(t *testing.T) {
	p := policy(t)
	readOnly := LeadRules{RoleSandbox: "read-only"}
	for _, tc := range []struct{ command, want string }{
		{`rg -n "a b" internal`, Allow},
		{`cat "a b"`, Allow},
		{`rg -n -F "a b" -A45 internal`, Allow},
		{`rg -n -F 'a b' -A45 internal`, Allow},
		{`grep -n "a b" -A14 internal/gate/gate.go | head -50`, Allow},
		{`grep -n 'a b' -A14 internal/gate/gate.go`, Allow},
		{`rg -n -F X -A45 internal 2>/dev/null`, Allow},
		{`rg -n x internal >/dev/null 2>&1`, Allow},
		{`rg -n x internal 2>/dev/null | head -5`, Allow},
		{`cat "$HOME/x"`, Deny},
		{`rg "a\"b" .`, Deny},
		{`rg "a	b" .`, Deny},
		{`rg "a *" .`, Deny},
		{`rg x . > out.txt`, Deny},
		{`rg x . >out.txt`, Deny},
		{`rg x . 2>err.txt`, Deny},
		{`rg x . >/dev/null2`, Deny},
		{`rg x . 2>/dev/null >out.txt`, Deny},
		{`cat ".duolog/bus.ndjson"`, Deny},
		{`cat ".duolog/bus ndjson"`, Deny},
		{`cat "secrets dir/.env"`, Deny},
	} {
		got := Evaluate(p, readOnly, mode.Full, Action{Kind: "exec", Target: tc.command, Role: "review_final"})
		if got.Kind != tc.want {
			t.Errorf("read-only %q: %s (%s), want %s", tc.command, got.Kind, got.Reason, tc.want)
		}
	}
	for _, command := range []string{`cat ".duolog/bus.ndjson"`, `cat ".duolog/bus ndjson"`} {
		got := Evaluate(p, readOnly, mode.Full, Action{Kind: "exec", Target: command, Role: "review_final"})
		if got.Rule != RuleState {
			t.Errorf("%q: %s (%s, %s), want a state deny", command, got.Kind, got.Reason, got.Rule)
		}
	}
	lead := LeadRules{RoleSandbox: "read-only", Deny: []config.Rule{{Action: "read", Match: "private/**", Decision: Deny}}}
	if got := Evaluate(p, lead, mode.Full, Action{Kind: "exec", Target: `cat "private/a b.txt"`, Role: "review_final"}); got.Kind != Deny {
		t.Errorf("a quoted path with a space must meet read denies: %s (%s)", got.Kind, got.Reason)
	}
	// Only read commands drop a /dev/null redirect; anything else still asks.
	for _, command := range []string{`go test ./... >/dev/null 2>&1`, `rm "a b"`} {
		if got := Evaluate(p, LeadRules{}, mode.Edits, Action{Kind: "exec", Target: command, Role: "implement"}); got.Kind != Ask {
			t.Errorf("writer %q: %s (%s), want ask", command, got.Kind, got.Reason)
		}
	}
	// The quoted word is one argument: the pattern is not a path, and a quoted path keeps its space.
	for _, tc := range []struct {
		command string
		want    []string
	}{
		{`rg -n "a b" internal`, []string{"internal"}},
		{`cat "src/my file.go" README.md`, []string{"src/my file.go", "README.md"}},
		{`grep 'x y' "src/a b.go" 2>/dev/null`, []string{"src/a b.go"}},
	} {
		// The lead guard looks inside uncertain commands, so ShellParts stays conservative.
		if _, uncertain := ShellParts(tc.command, ""); !uncertain {
			t.Errorf("ShellParts %q is certain", tc.command)
		}
		parts, uncertain := readShellParts(tc.command, "")
		if uncertain || len(parts) != 1 {
			t.Errorf("%q: %q, uncertain %v", tc.command, parts, uncertain)
			continue
		}
		if targets, reason := readCommandTargets(parts[0]); !slices.Equal(targets, tc.want) || reason != "" {
			t.Errorf("%q: %q (%s), want %q", tc.command, targets, reason, tc.want)
		}
	}
}

// A quoted rg glob or path with spaces reaches the same read denies and state
// deny as an unquoted one: rg receives the space, so the gate must compare it.
func TestQuotedGlobsAndPathsMeetReadDenies(t *testing.T) {
	p := policy(t)
	denies := []config.Rule{
		{Action: "read", Match: "private notes.txt", Decision: Deny},
		{Action: "read", Match: "secret dir/**", Decision: Deny},
		{Action: "read", Match: "private_notes.txt", Decision: Deny},
		{Action: "read", Match: "secret_dir/**", Decision: Deny},
	}
	for _, tc := range []struct{ command, unquoted, want string }{
		// A glob that can reach a denied file asks, as its unquoted twin does.
		{`rg -n -F "a b" -g "private notes.txt" .`, `rg -n -F a -g private_notes.txt .`, Ask},
		{`rg -n -F 'a b' -g 'private notes.txt' .`, `rg -n -F a -g private_notes.txt .`, Ask},
		{`rg -n -F "a b" --glob "private notes.txt" .`, `rg -n -F a --glob private_notes.txt .`, Ask},
		{`rg -n -F "a b" --iglob="Private Notes.txt" .`, `rg -n -F a --iglob=Private_Notes.txt .`, Ask},
		{`rg -n -F "a b" -g "secret dir" .`, `rg -n -F a -g secret_dir .`, Ask},
		// A path operand meets the deny itself.
		{`rg -n -F "a b" "private notes.txt"`, `rg -n -F a private_notes.txt`, Deny},
		{`rg -n -F "a b" "secret dir"`, `rg -n -F a secret_dir`, Deny},
		{`cat "secret dir/a b.txt"`, `cat secret_dir/a_b.txt`, Deny},
		// Other quoted globs and paths with spaces are still plain reads.
		{`rg -n -F "a b" -g "public notes.txt" .`, `rg -n -F a -g public_notes.txt .`, Allow},
		{`rg -n -F "a b" "public notes.txt"`, `rg -n -F a public_notes.txt`, Allow},
	} {
		for _, lead := range []struct {
			name  string
			rules LeadRules
			m     mode.Mode
			role  string
		}{
			{"read-only", LeadRules{RoleSandbox: "read-only", Deny: denies}, mode.Full, "review_final"},
			{"writer", LeadRules{Deny: denies}, mode.Edits, "implement"},
		} {
			twin := Evaluate(p, lead.rules, lead.m, Action{Kind: "exec", Target: tc.unquoted, Role: lead.role})
			got := Evaluate(p, lead.rules, lead.m, Action{Kind: "exec", Target: tc.command, Role: lead.role})
			if got.Kind != tc.want || twin.Kind != tc.want {
				t.Errorf("%s %q: %s (%s), unquoted %s (%s), want %s", lead.name, tc.command, got.Kind, got.Reason, twin.Kind, twin.Reason, tc.want)
			}
		}
	}
	// A quoted .duolog glob opens the state folder to rg, as an unquoted one does.
	work := t.TempDir()
	for _, command := range []string{
		`rg -n -F "a b" -g ".duolog" .`,
		`rg -n -F "a b" -g ".duolog/**" .`,
		`rg -n -F "a b" -g "a b/.duolog" .`,
		`rg -n -F "a b" --glob=".DUOLOG" .`,
		`rg -n -F "a b" ".duolog/bus ndjson"`,
	} {
		got := Evaluate(p, LeadRules{RoleSandbox: "read-only"}, mode.Full, Action{Kind: "exec", Target: command, Role: "review_final", Work: work, Cwd: work, State: work})
		if got.Kind != Deny || got.Rule != RuleState {
			t.Errorf("%q: %s (%s, %s), want a state deny", command, got.Kind, got.Reason, got.Rule)
		}
	}
}

func TestSafeSedPrintsOnly(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct{ command, want string }{
		{"sed -n 1,200p src/a.go", Allow},
		{"sed -n '1,$p' src/a.go src/b.go", Allow},
		{"sed -n 5p src/a.go", Allow},
		{"sed -n 1,5p .env", Deny},
		{"sed -n 1w src/out src/a.go", Ask},
		{"sed -i s/a/b/ src/a.go", Ask},
		{"sed -n -e 1p src/a.go", Ask},
		{"sed 1p src/a.go", Ask},
	} {
		got := Evaluate(p, LeadRules{}, mode.Edits, Action{Kind: "exec", Target: tc.command, Role: "implement"})
		if got.Kind != tc.want {
			t.Errorf("%q: %s (%s), want %s", tc.command, got.Kind, got.Reason, tc.want)
		}
	}
	// Full mode keeps its old answer for a sed that writes: on_unmatched allows it.
	got := Evaluate(p, LeadRules{}, mode.Full, Action{Kind: "exec", Target: "sed -i s/a/b/ src/a.go", Role: "implement"})
	if got.Kind != Allow {
		t.Errorf("full sed -i: %s (%s), want allow", got.Kind, got.Reason)
	}
}

// The value of -g, -A or --include is neither the search pattern nor a path;
// only the value of -f/--file is a file to read.
func TestRgValueOptionsAndFiles(t *testing.T) {
	for _, tc := range []struct {
		command string
		targets []string
	}{
		{"rg --files src -g x", []string{"src"}},
		{"rg --files .env", []string{".env"}},
		{"rg -A 3 needle src", []string{"src"}},
		{"grep --include x needle src/a.go", []string{"src/a.go"}},
	} {
		targets, reason := readCommandTargets(tc.command)
		if reason != "" || len(targets) != len(tc.targets) {
			t.Errorf("%q: %v (%s), want %v", tc.command, targets, reason, tc.targets)
			continue
		}
		for i := range targets {
			if targets[i] != tc.targets[i] {
				t.Errorf("%q: %v, want %v", tc.command, targets, tc.targets)
			}
		}
	}
}

// Type-check scripts are allowed by name only; npx is not allowed by default.
func TestCheckCommandRules(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct{ command, want string }{
		// npx can resolve packages from the registry even with --no: a project opts in by its own rule.
		{"npx --no vitest run src/a.test.ts", Ask},
		{"npx vitest run src/a.test.ts", Ask},
		{"npm run type", Allow},
		{"npm run typecheck", Allow},
		{"npm run type:check", Allow},
		{"npm run typeorm:migrate", Ask},
	} {
		got := Evaluate(p, LeadRules{}, mode.Edits, Action{Kind: "exec", Target: tc.command, Role: "implement"})
		if got.Kind != tc.want {
			t.Errorf("%q: %s (%s), want %s", tc.command, got.Kind, got.Reason, tc.want)
		}
	}
}

// jq reads files after its filter; options that read other files, write or
// run a program are refused. Read-only roles get the same answers.
func TestMoreReadCommands(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct{ command, want string }{
		{"jq .name package.json", Allow},
		{"jq -r --arg k v .name package.json", Allow},
		{"jq --indent 4 . package.json", Allow},
		{"jq .key .env", Deny},
		{"jq --arg k v .key .env", Deny},
		// env and $ENV print the worker's environment, API keys included.
		{"jq -n env", Ask},
		{"jq -nr env.OPENAI_API_KEY", Ask},
		{"jq -n '$ENV'", Ask},
		{"jq -n '$__prog_args'", Ask},
		// -X is a flag in BSD grep and ugrep: the file after it is still a target.
		{"grep -X x .env", Deny},
		{"grep -nX x .env", Deny},
		// ugrep runs programs with --filter and writes with --save-config.
		{"grep --filter=all:cat x src/a.go", Ask},
		{"grep --save-config", Ask},
		{"grep -n -A 3 -e x src/a.go", Allow},
		{"grep -3 --color=never x src/a.go", Allow},
		// jq prints the program it failed to parse, so -f would print a secret.
		{"jq -f .env", Deny},
		{"jq -nf .env", Deny},
		{"jq --from-file .env", Deny},
		{"jq -f prog.jq src/a.json", Ask},
		{"jq -L secrets -n .", Deny},
		{"jq --rawfile x .env -n .", Deny},
		{"yq .services docker-compose.yml", Ask},
		{"diff -r src tests", Ask},
		{"sort src/a.txt", Allow},
		{"sort -rn -k 2 -t , src/a.txt", Allow},
		{"sort -k2,2 --field-separator=, src/a.txt", Allow},
		{"sort -o src/a.txt src/b.txt", Ask},
		// -n then -o with the next word: a cluster can hide the output file.
		{"sort -no src/a.txt src/b.txt", Ask},
		{"sort --compress-program=sh src/a.txt", Ask},
		{"sort --comp=sh src/a.txt", Ask},
		{"sort -T /tmp src/a.txt", Ask},
		{"sort src/a.txt .env", Deny},
		{"uniq src/a.txt", Allow},
		{"uniq src/a.txt src/b.txt", Ask},
		// - is an operand too: uniq - OUT writes OUT, such as the learned rules.
		{"uniq - .duolog/learned.yaml", Deny},
		{"uniq -- - .git/config", Deny},
		{"uniq - src/out.txt", Ask},
		{"uniq -c -f 1 src/a.txt", Allow},
		{"uniq --group=append src/a.txt", Allow},
		{"tree -L 2 src", Allow},
		{"tree -o out.txt src", Ask},
		{"tree -ao out.txt src", Ask},
		{"tree -RH . src", Ask},
		{"file src/a.go", Allow},
		{"file -C -m src/magic", Ask},
		// -m and -f read a file and print its lines in errors, also when attached.
		{"file -m.env src/a.go", Ask},
		{"file -f.env", Ask},
		{"file -bm.env src/a.go", Ask},
		{"file --magic-file=src/m src/a.go", Ask},
		{"file -bi src/a.go", Allow},
		{"stat src/a.go", Allow},
		{"which go", Allow},
	} {
		got := Evaluate(p, LeadRules{}, mode.Edits, Action{Kind: "exec", Target: tc.command, Role: "implement"})
		if got.Kind != tc.want {
			t.Errorf("%q: %s (%s), want %s", tc.command, got.Kind, got.Reason, tc.want)
		}
		readOnly := tc.want
		if readOnly == Ask {
			readOnly = Deny
		}
		got = Evaluate(p, LeadRules{RoleSandbox: "read-only"}, mode.Full, Action{Kind: "exec", Target: tc.command, Role: "review_final"})
		if got.Kind != readOnly {
			t.Errorf("read-only %q: %s (%s), want %s", tc.command, got.Kind, got.Reason, readOnly)
		}
	}
}

// A Codex worker that asks the user would wait for an answer that never comes.
func TestWorkerCannotAskTheUser(t *testing.T) {
	for _, tool := range []string{"request_user_input_async", "AskUserQuestion"} {
		got := Evaluate(policy(t), LeadRules{}, mode.Full, Action{Kind: "unknown", Target: tool, Role: "implement"})
		if got.Kind != Deny || !strings.Contains(got.Reason, "needs") {
			t.Fatalf("%s: got %s (%s)", tool, got.Kind, got.Reason)
		}
	}
}

// Writer roles may run the usual check tools of each ecosystem; package runners
// that can download code, and generators, still ask.
func TestEcosystemCheckCommands(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct{ command, want string }{
		{"node_modules/.bin/eslint src/a.ts", Allow},
		{"./node_modules/.bin/tsc --noEmit -p tsconfig.json", Allow},
		{".venv/bin/ruff check app/a.py", Allow},
		{"python -m pytest tests/a.py", Allow},
		{"go vet ./...", Allow},
		{"gofmt -l .", Allow},
		{"golangci-lint run ./...", Allow},
		{"cargo clippy --offline", Allow},
		{"clang-format --dry-run --Werror src/a.cc", Allow},
		{"cmake --build build", Allow},
		{"./gradlew --offline test", Allow},
		{"mvn -o verify", Allow},
		{"dotnet test --no-restore", Allow},
		// A program path that climbs out of an allowed folder is not that tool.
		{"node_modules/.bin/tsc/../../../usr/bin/curl example.com", Ask},
		{".venv/bin/ruff/../../x", Ask},
		{"npx eslint src", Ask},
		{"go generate ./...", Ask},
		{"go run ./cmd/x", Ask},
		{"./gradlew publish", Ask},
	} {
		got := Evaluate(p, LeadRules{}, mode.Edits, Action{Kind: "exec", Target: tc.command, Role: "implement"})
		if got.Kind != tc.want {
			t.Errorf("%q: %s (%s), want %s", tc.command, got.Kind, got.Reason, tc.want)
		}
	}
}

func TestPatternValuesAreNotPaths(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct{ command, want string }{
		{"rg -n -F -e unfinished -e /orders/ src", Allow},
		{"grep -n -e /etc/passwd src/a.go", Allow},
		{"rg -f .env src", Deny},
		{"grep --file .env src/a.go", Deny},
	} {
		got := Evaluate(p, LeadRules{}, mode.Edits, Action{Kind: "exec", Target: tc.command, Role: "implement"})
		if got.Kind != tc.want {
			t.Errorf("%q: %s (%s), want %s", tc.command, got.Kind, got.Reason, tc.want)
		}
	}
}

// A read command that writes would let a read-only role or plan mode change
// protected files, such as the learned rules that widen the policy.
func TestReadCommandsCannotWriteInPlanMode(t *testing.T) {
	p := policy(t)
	for _, command := range []string{"uniq - .duolog/learned.yaml", "uniq -- - .duolog/brief.md", "sort -no .git/config src/a.txt", "file -m.env src/a.go"} {
		for _, m := range []mode.Mode{mode.Plan, mode.Edits, mode.Full} {
			if got := Evaluate(p, LeadRules{RoleSandbox: "read-only"}, m, Action{Kind: "exec", Target: command, Role: "review_final"}); got.Kind == Allow {
				t.Errorf("read-only %s %q: allowed", m, command)
			}
			if got := Evaluate(p, LeadRules{}, m, Action{Kind: "exec", Target: command, Role: "implement"}); m == mode.Plan && got.Kind == Allow {
				t.Errorf("plan %q: allowed", command)
			}
		}
	}
}
