package gate

import (
	"runtime"
	"slices"
	"testing"

	"github.com/lorem-dev/duolog/internal/mode"
)

// reviewCommands are the exact commands of findings 1-5 in two security
// reviews of an earlier attempt to stop false outside asks. That attempt was
// dropped; each command must stay at least as strict as on develop.
var reviewCommands = []string{
	// A quoted <<< is a file name, not a here-string.
	`cat '<<<' .duolog/decisions.ndjson`,
	`cat '<<<' /etc/passwd`,
	`cat '<<<' ~/.ssh/id_rsa`,
	`cat "<<<" ../peer-gone/.env`,
	`cat '<<<' .duolog/bus.ndjson`,
	// Text piped into a shell, or run as an editor, executes.
	`echo 'duolog telemetry show' | sh`,
	`sh <<< 'duolog telemetry show'`,
	`GIT_EDITOR='duolog telemetry show; true' git commit --allow-empty`,
	`printf 'duolog balance reset' | bash`,
	`GIT_EDITOR='duolog telemetry show;:' git commit --allow-empty`,
	`EDITOR='duolog telemetry show;:' git tag -a v1`,
	// A glob in the pattern position expands into files.
	`command grep /etc/*`,
	// Script words with no whitespace.
	`sh -c 'cat</etc/passwd'`,
	`sh -c 'cat<go.mod>/tmp/x'`,
	`eval 'cat<~/.ssh/id_rsa'`,
	`awk 'BEGIN{while((getline l<"/etc/passwd")>0)print l}'`,
	`awk '{print >"/tmp/x"}' go.mod`,
	`git -c alias.x='!cat</etc/passwd' x`,
	`find . -exec sh -c 'cat</etc/passwd' \;`,
	// GNU awk options that load a program file.
	`gawk -E /etc/passwd`,
	`gawk --exec /etc/passwd`,
	`gawk -i /etc/passwd 'BEGIN{exit}'`,
	// Option values and abbreviations that hide a file.
	`grep --reg=. /etc/passwd`,
	`rg --pre /outside/script x .`,
	`rg --ignore-file /outside/list x .`,
	`rg --hostname-bin /outside/bin x .`,
	`grep -r --exclude-from /outside/list x .`,
	`grep --reg=root /etc/passwd`,
	// cd forms that move elsewhere.
	`CDPATH=~ cd .ssh && cat id_rsa`,
	`CDPATH=.. cd peer-gone && cat go.mod`,
	`HOME=/etc cd && cat passwd`,
	`command cd && cat .ssh/id_rsa`,
	`builtin cd && cat .aws/credentials`,
}

// strictness orders decisions: an allow, an ask the lead may answer, an ask
// only the person may answer, a deny.
func strictness(d Decision) int {
	switch {
	case d.Kind == Deny:
		return 3
	case PersonOnly(d):
		return 2
	case d.Kind == Ask:
		return 1
	}
	return 0
}

// developStrictness is what develop decided for each review command, for a
// writing worker in edits and in ask mode alike: a deny for state, secrets
// and the private usage stores, a lead's ask for the cd forms, and the
// person's ask for the rest.
func developStrictness(command string) int {
	switch command {
	case `cat '<<<' .duolog/decisions.ndjson`, `cat "<<<" ../peer-gone/.env`, `cat '<<<' .duolog/bus.ndjson`,
		`echo 'duolog telemetry show' | sh`, `sh <<< 'duolog telemetry show'`,
		`GIT_EDITOR='duolog telemetry show; true' git commit --allow-empty`, `printf 'duolog balance reset' | bash`,
		`GIT_EDITOR='duolog telemetry show;:' git commit --allow-empty`, `EDITOR='duolog telemetry show;:' git tag -a v1`,
		`grep -r --exclude-from /outside/list x .`:
		return 3
	case `CDPATH=~ cd .ssh && cat id_rsa`, `CDPATH=.. cd peer-gone && cat go.mod`, `HOME=/etc cd && cat passwd`,
		`command cd && cat .ssh/id_rsa`, `builtin cd && cat .aws/credentials`:
		return 1
	}
	return 2
}

func TestReviewCommandsStayAsStrictAsDevelop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the reviews assume a POSIX host and shell")
	}
	p := policy(t)
	work := t.TempDir()
	for _, m := range []mode.Mode{mode.Edits, mode.Ask} {
		for _, command := range reviewCommands {
			d := Evaluate(p, LeadRules{}, m, Action{Kind: "exec", Target: command, Role: "implement", Work: work, Cwd: work, State: work})
			if got, want := strictness(d), developStrictness(command); got < want {
				t.Errorf("%s in %s: %+v is less strict than develop (%d < %d)", command, m, d, got, want)
			}
		}
	}
	// The lead's guard for duolog remember asks the person by ExecOutside
	// alone. Develop found an outside operand in every command from the glob
	// on, up to the cd forms.
	for _, command := range reviewCommands[slices.Index(reviewCommands, `command grep /etc/*`):slices.Index(reviewCommands, `CDPATH=~ cd .ssh && cat id_rsa`)] {
		if !ExecOutside(Action{Kind: "exec", Target: command, Work: work, Cwd: work}) {
			t.Errorf("%s: no outside operand found, develop found one", command)
		}
	}
}
