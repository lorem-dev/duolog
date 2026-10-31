package gate

import (
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
)

// The read-command allow rule must not grant anything that only starts with a
// read command, or that uses a read command to write or run something.
func TestReadCommandRuleGrantsOnlyReads(t *testing.T) {
	p := policy(t)
	for _, command := range []string{"find . -delete", "find . -exec rm {} +", "catastrophe --wipe", "tailscale down", "lsof -k", "findmnt", "git diff --output=out.txt"} {
		check(t, p, LeadRules{}, mode.Ask, Action{Kind: "exec", Target: command, Role: "implement"}, Ask)
	}
	for _, command := range []string{"cat file", "find . -name x -type f", "rg pattern src", "grep -n x file", "head -n 5 file", "ls"} {
		check(t, p, LeadRules{}, mode.Ask, Action{Kind: "exec", Target: command, Role: "implement"}, Allow)
	}
}

func TestReadCommandLiteralRuleStaysTheUsersChoice(t *testing.T) {
	p := policy(t)
	p.Rules = append(p.Rules[:0:0], p.Rules...)
	p.Rules[len(p.Rules)-1].Match = "find . -delete"
	p.Rules[len(p.Rules)-1].Action = "exec"
	p.Rules[len(p.Rules)-1].Decision = Allow
	check(t, p, LeadRules{}, mode.Ask, Action{Kind: "exec", Target: "find . -delete", Role: "implement"}, Allow)
}

// Reads by command honour the same rules as the Read tool.
func TestReadCommandsHonourReadRules(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct {
		role    string
		m       mode.Mode
		command string
		want    string
	}{
		{"review_final", mode.Full, "cat .env", Deny},
		{"review_final", mode.Full, "cat apps/web/.env", Deny},
		{"review_final", mode.Full, "head -c 100 .env", Deny},
		{"review_final", mode.Full, "grep SECRET .env", Deny},
		{"review_final", mode.Full, "grep -e SECRET .env", Deny},
		{"review_final", mode.Full, "grep -f.env other", Deny},
		{"review_final", mode.Full, "grep --file=.env other", Deny},
		{"review_final", mode.Full, "wc --files0-from=.env", Deny},
		{"review_final", mode.Full, `cat ".env"`, Deny},
		{"review_final", mode.Full, "cat ok.txt && cat .env", Deny},
		{"review_final", mode.Full, "cat ~/.ssh/id_rsa", Ask},
		{"review_final", mode.Full, "cat /etc/passwd", Ask},
		{"review_final", mode.Full, "cat ../other", Ask},
		{"review_final", mode.Full, "ls ~", Ask},
		{"review_final", mode.Full, "find /etc -name passwd", Ask},
		{"implement", mode.Ask, "cat .env", Deny},
		{"implement", mode.Ask, "cat ~/.ssh/id_rsa", Ask},
		{"implement", mode.Plan, "cat .env", Deny},
		// A shell glob can expand onto a denied file, so it cannot be allowed.
		{"implement", mode.Ask, "cat .e*", Ask},
		{"implement", mode.Full, "cat secrets/*", Ask},
		// Recursion reaches denied files without naming them.
		{"review_final", mode.Full, "grep -r SECRET .", Ask},
		{"review_final", mode.Full, "grep -rn SECRET src", Ask},
		{"review_final", mode.Full, "grep --recursive SECRET .", Ask},
		{"review_final", mode.Full, "rg --hidden SECRET", Ask},
		{"review_final", mode.Full, "rg -uu SECRET", Ask},
		{"review_final", mode.Full, "rg --no-ignore SECRET", Ask},
		// The pattern is not a path, and options are not paths.
		{"review_final", mode.Full, "grep .env README.md", Allow},
		{"review_final", mode.Full, "rg /etc/passwd src", Allow},
		{"review_final", mode.Full, "grep -n x src/a.go", Allow},
		{"review_final", mode.Full, "grep --include=*.go x src/a.go", Allow},
		{"review_final", mode.Full, "find . -name .env", Allow},
		{"review_final", mode.Full, "ls -la src", Allow},
	} {
		lead := LeadRules{}
		if tc.role == "review_final" {
			lead.RoleSandbox = "read-only"
		}
		got := Evaluate(p, lead, tc.m, Action{Kind: "exec", Target: tc.command, Role: tc.role})
		if got.Kind != tc.want {
			t.Errorf("%s %s %q: %s (%s), want %s", tc.role, tc.m, tc.command, got.Kind, got.Reason, tc.want)
		}
	}
}

func TestReadCommandsHonourALeadDeny(t *testing.T) {
	p := policy(t)
	lead := LeadRules{Deny: []config.Rule{{Action: "read", Match: "private/**", Decision: Deny}}}
	got := Evaluate(p, lead, mode.Full, Action{Kind: "exec", Target: "cat private/key.txt", Role: "implement"})
	if got.Kind != Deny {
		t.Fatal(got)
	}
}

// An absolute path into the project is an ordinary project read.
func TestReadCommandPathsResolveFromTheWorkingFolder(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	for _, tc := range []struct {
		cwd, word, want string
	}{
		{root, filepath.Join(root, "a.go"), Allow},
		{root, filepath.Join(root, ".env"), Deny},
		{root, filepath.Join(filepath.Dir(root), "other"), Ask},
		{sub, "a.go", Allow},
		{sub, ".env", Deny},
		{sub, "../../other", Ask},
	} {
		command := "cat " + filepath.ToSlash(tc.word)
		got := Evaluate(p, LeadRules{}, mode.Full, Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: tc.cwd})
		if got.Kind != tc.want {
			t.Errorf("%s in %s: %s (%s), want %s", tc.word, tc.cwd, got.Kind, got.Reason, tc.want)
		}
	}
}

func TestReadOnlyFindAndRgAllowOnlyKnownOptions(t *testing.T) {
	p := policy(t)
	lead := LeadRules{RoleSandbox: "read-only"}
	for _, command := range []string{
		"find . -fprint0 x", "find . -fprint x", "find . -fprintf x y", "find . -fls x", "find . -ok rm", "find . -okdir rm", "find . -execdir rm", "find . -anything",
		"rg --hostname-bin=./evil x", "rg --pre=evil x", "rg --pre evil x", "rg --pre-glob=* x", "rg -z x", "rg -nz x", "rg --search-zip x", "rg --anything x",
	} {
		check(t, p, lead, mode.Full, Action{Kind: "exec", Target: command, Role: "review_final"}, Deny)
	}
	for _, command := range []string{
		"find . -name x -type f -maxdepth 2", "find -L . -mtime -1 -print", "find . -not -path x", "find . -newermt x",
		"rg pattern", "rg -n x src", "rg -in x", "rg -A3 x", "rg -g x y", "rg --glob=x y", "rg -t go x", "rg --type=go -c x", "rg -- --pre=x src",
	} {
		check(t, p, lead, mode.Full, Action{Kind: "exec", Target: command, Role: "review_final"}, Allow)
	}
}

func TestGlobAndGrepPatternsAreReadTargets(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct {
		input, want string
	}{
		{`{"tool_name":"Glob","tool_input":{"pattern":"../../**"}}`, Ask},
		{`{"tool_name":"Glob","tool_input":{"pattern":"**/*.go"}}`, Allow},
		{`{"tool_name":"Glob","tool_input":{"pattern":".env"}}`, Deny},
		{`{"tool_name":"Grep","tool_input":{"pattern":"SECRET","glob":".env"}}`, Deny},
		{`{"tool_name":"Grep","tool_input":{"pattern":"../x"}}`, Allow},
		{`{"tool_name":"Grep","tool_input":{"pattern":"x","glob":"../**"}}`, Ask},
	} {
		actions, err := NormalizeAll("claude", []byte(tc.input))
		if err != nil {
			t.Fatal(err)
		}
		got := Allow
		for _, a := range actions {
			a.Role = "implement"
			d := Evaluate(p, LeadRules{}, mode.Full, a)
			if d.Kind == Deny || got == Allow {
				got = d.Kind
			}
		}
		if got != tc.want {
			t.Errorf("%s: %s, want %s (%d actions)", tc.input, got, tc.want, len(actions))
		}
	}
}
