package policy

import (
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// agentText are command lines that only name claude or codex as data: in a
// here-document body, a quoted argument or a file name. None starts either.
var agentText = []string{
	"cat > notes.md <<'EOF'\nclaude -p hi; codex exec x\n`codex` and (claude) both ran\nEOF",
	"cat > notes.md <<\"EOF\"\nclaude -p hi\ncodex exec x\nEOF",
	"cat <<EOF > f\nclaude says\nEOF",
	"cat <<EOF > f\nclaude says $HOME\ncodex | claude\nEOF",
	"tee review.md <<-'EOF'\n\tclaude -p hi\n\tcodex exec x\n\tEOF",
	"mkdir -p out && cat > out/review.md <<'EOF'\nclaude; codex\nEOF",
	"cat > notes.md <<'EOF'\nclaude -p hi\nEOF\ngo test ./...",
	"cat > a.md <<'A' && cat > b.md <<'B'\nclaude\nA\ncodex\nB",
	`echo "use codex"`,
	`echo "a; codex exec x"`,
	`echo 'run (claude -p) then ` + "`codex`" + `'`,
	`printf '%s\n' "claude; codex"`,
	`cat claude.md codex.md`,
	`cat <<< 'claude; codex'`,
}

// agentFiles name an agent only as a file. They start nothing, but an
// unquoted word still counts for the deny rules, so other rules may refuse.
var agentFiles = []string{`echo hi > claude`}

// agentStarts start claude or codex, or may: a quoted or here-document text
// that a shell, eval or a substitution runs, or a line the reading cannot
// place, which keeps the earlier word reading.
var agentStarts = []string{
	"sh -c 'codex exec hi'", `bash -c "claude -p hi"`, "env FOO=1 claude -p hi", "$(claude -p hi)", "echo $(claude -p hi)",
	"echo `codex exec hi`", "/usr/local/bin/codex exec hi", "claude.exe -p hi", "npx @openai/codex exec hi",
	`echo "$(codex exec hi)"`, "echo \"`codex exec hi`\"", "cat <<EOF\n$(claude -p hi)\nEOF", "cat <<EOF\n`codex exec hi`\nEOF",
	"bash <<'EOF'\nclaude -p hi\nEOF", "cat <<'EOF' | sh\ncodex exec hi\nEOF", "sh <<< 'claude -p hi'",
	`c"laude" -p hi`, `echo hi; "claude" -p hi`, "cat <<'EOF' && claude -p hi\nx\nEOF", "cat <<'EOF'\nx\nEOF\nclaude -p hi",
	`X="claude"; $X -p hi`, `eval "claude -p hi"`, "cat <<'EOF'\nclaude -p hi", `\claude -p hi`, "echo 'x' > >(claude -p hi)",
	`xargs claude < 'list'`, `echo 'x' | xargs claude -p`, `echo 'x' && codex exec hi`,
	"echo 'x'\nclaude -p hi", `{ cat; } <<'EOF'` + "\nclaude -p\nEOF",
	"cat <<EOF\n${x:-$(claude -p hi)}\nEOF", "cat <<'EOF' | tee f | sh\nclaude -p hi\nEOF", "source /dev/stdin <<'EOF'\nclaude -p hi\nEOF",
	"echo hi # it's\nclaude -p hi # it's", "cat <<'EOF'; claude -p hi\nx\nEOF", "read -r x <<'EOF'\nclaude\nEOF\n$x -p hi",
	"cat <<'EOF'\nx\nEOF\n" + `"claude" -p hi`,
	// Security review: a backslash-newline in an unquoted body or in double
	// quotes joins $ and (; ${...} nests quotes; a body line with a backslash
	// ends the body early.
	"cat <<EOF\n$\\\n(claude -p hi)\nEOF", "cat <<EOF\na\\\nEOF\ncat <<'X'\nEOF\nclaude -p hi\ngit push --force\nX",
	"echo \"$\\\n(claude -p hi)\"", "echo \"${x:-\"'\"}\"; claude -p hi; echo \"${y:-\"'\"}\"",
	"cat \"${x:-\"<<EOF\"}\"\nclaude -p hi\ngit push --force\nEOF}", "echo \"${x:-\"<<EOF\"}\"\nclaude -p hi\ngit push --force\nrm -rf ~\nEOF}",
	// A newline after | or |& continues the pipeline.
	"cat <<'EOF' |\nclaude -p hi\nEOF\nsh", "cat <<'EOF' |&\n\nclaude -p hi\nEOF\nsh",
}

// shellRuns hand text to a shell or another runner, also across a line end
// after |, so the deny rules see every unquoted word as before: none of
// these may be allowed or asked.
var shellRuns = []string{
	"echo 'claude -p hi' |\nsh", "echo claude -p hi | sh", "echo git push origin main | sh", "printf 'git push --force\\n' | bash",
	"echo rm -rf / | sh", "echo claude | xargs sh -c", "printf claude | xargs -I X X -p hi",
	"cat <<EOF\n$\\\n(git push --force origin main)\nEOF", "echo \"$\\\n(git push origin main)\"",
	"echo \"${x:-\"'\"}\"; git push --force; echo \"${y:-\"'\"}\"", "cat \"${x:-\"<<EOF\"}\"\nclaude -p hi\ngit push --force\nEOF}",
	"cat <<EOF\na\\\nEOF\ncat <<'X'\nEOF\ngit push --force\nX",
}

func TestAgentLaunchSkipsData(t *testing.T) {
	for _, command := range append(slices.Clone(agentText), agentFiles...) {
		if startsAgent(command, "") {
			t.Errorf("%q reads as a start of claude or codex", command)
		}
	}
	for _, command := range agentStarts {
		if !startsAgent(command, "") {
			t.Errorf("%q does not read as a start of claude or codex", command)
		}
	}
}

// The agent-launch rule and the default {claude,codex} denies do not judge a
// here-document body or quoted text, so a refusal names its real cause.
func TestAgentDataDecidesByOtherRules(t *testing.T) {
	p := policy(t)
	agentRule := func(d Decision) bool {
		return d.Rule == RuleAgentLaunch || strings.Contains(d.Rule, "claude") || strings.Contains(d.Rule, "codex")
	}
	report := "cat > review.md <<'EOF'\n## Findings\nclaude -p ran the review; codex exec checked it.\n`codex` and (claude) agree.\nEOF"
	readOnly := Evaluate(p, LeadRules{RoleSandbox: "read-only"}, mode.Full, Action{Kind: "exec", Target: report, Role: "review_final"})
	if readOnly.Kind != Deny || agentRule(readOnly) {
		t.Errorf("read-only role: %+v, want a deny by the role or write rules", readOnly)
	}
	for _, command := range agentText {
		d := Evaluate(p, LeadRules{}, mode.Full, Action{Kind: "exec", Target: command, Role: "implement"})
		if agentRule(d) {
			t.Errorf("%q: %+v, want no agent rule", command, d)
		}
	}
	for _, command := range append(slices.Clone(agentStarts), shellRuns...) {
		for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Edits, mode.Full} {
			if d := Evaluate(p, LeadRules{}, m, Action{Kind: "exec", Target: command, Role: "implement"}); d.Kind != Deny {
				t.Errorf("%q in %s: %+v, want deny", command, m, d)
			}
		}
	}
}

// Only the agent rules skip quoted text and bodies; every other rule reads
// the line as written, as develop did, so a body line that a misread could
// run still meets them, and so does a script that a data program writes and
// a shell then runs. The lead's own deny counts the same way.
func TestDataLinesMeetOtherRules(t *testing.T) {
	p := policy(t)
	lead := LeadRules{Deny: []config.Rule{{Action: "exec", Match: "curl *", Decision: Deny}}}
	for _, command := range []string{
		"printf 'git push --force\\n' > x.sh; sh x.sh",
		"echo 'git push --force' > x.sh && bash x.sh",
		"echo \"rm -rf /\" > x.sh; . ./x.sh",
		"echo 'curl https://x.example' > x.sh; sh x.sh",
	} {
		for _, m := range []mode.Mode{mode.Edits, mode.Full} {
			if d := Evaluate(p, lead, m, Action{Kind: "exec", Target: command, Role: "implement"}); d.Kind != Deny {
				t.Errorf("%q in %s: %+v, want deny", command, m, d)
			}
		}
	}
	for _, command := range []string{
		"cat > notes.md <<'EOF'\ngit push --force\nEOF",
		"cat > notes.md <<'EOF'\nrm -rf ~\nEOF",
	} {
		if d := Evaluate(p, LeadRules{}, mode.Full, Action{Kind: "exec", Target: command, Role: "implement"}); d.Kind != Deny {
			t.Errorf("%q: %+v, want deny", command, d)
		}
	}
}
