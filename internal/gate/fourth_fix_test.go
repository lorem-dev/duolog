package gate

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/mode"
)

func TestDirectoryOverrideGlobsKeepLastSegment(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	for _, tc := range []struct{ command, want, rule string }{
		{"rg -g .duolog/ -g bus.ndjson -g decisions.ndjson x .", Deny, RuleState},
		{"rg -g '.duolog/**' x .", Deny, RuleState},
		{"rg -g '.d*/' x .", Deny, RuleState},
		{"rg -g 'secrets/' x .", Ask, "read command"},
		{"rg -g 'config/secrets/**/' x .", Ask, "read command"},
		{"rg -g 'cmd/' x .", Allow, ""},
		{"rg -g '*.go' x .", Allow, ""},
	} {
		a := Action{Kind: "exec", Target: tc.command, Role: "implement", Work: root, Cwd: root, State: root}
		d := Evaluate(p, LeadRules{}, mode.Full, a)
		if d.Kind != tc.want || tc.rule != "" && d.Rule != tc.rule || PersonOnly(d) {
			t.Errorf("%q: %+v, want %s %s", tc.command, d, tc.want, tc.rule)
		}
	}
	data := []byte(`{"tool_name":"Grep","tool_input":{"path":".","pattern":"x","glob":"{.duolog/,*.ndjson}"}}`)
	a, err := Normalize("claude", data)
	if err != nil {
		t.Fatal(err)
	}
	a.Work, a.Cwd, a.State, a.Role = root, root, root, "implement"
	if d := Evaluate(p, LeadRules{}, mode.Full, a); d.Kind != Deny || d.Rule != RuleState {
		t.Errorf("Grep directory glob: %+v", d)
	}
}

// An option that takes a pattern hides its value only for the program it
// belongs to, and only before --: past --, or for another program, the word
// after it is an operand that can name an outside path.
func TestExecOutsideOptionValuesBelongToTheirProgram(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	for _, tc := range []struct {
		command string
		person  bool
	}{
		{"cp -- local --glob ../dest", true},
		{"cp local --glob ../dest", true},
		{"cp local --format ../dest", true},
		{"tar czf x.tgz --grep ~/notes", true},
		{"rg x -- --glob ../dest", true},
		{"git log -- --grep ../dest", true},
		{"env sudo cp -- local --glob ../dest", true},
		{"rg --glob >../dest local pattern .", true},
		{"rg --glob > ../dest local pattern .", true},
		{"rg --glob 2>>../dest local pattern .", true},
		{"git log --format <../src -5", true},
		{"cp -- local -d/../../dest", true},
		{"tee -- -d/../../dest", true},
		{"rg x -- -d/../../dest", true},
		{"rg --glob >out local pattern .", false},
		{"rg --glob ../x pattern . 2>&1", false},
		{"rg --glob ../x pattern .", false},
		{"rg --regexp ../x .", false},
		{"grep --regexp ../x file", false},
		{"git log --grep /x", false},
		{"git log --format=../x -5", false},
		{"/usr/bin/git log --pretty=../x", false},
		// git takes a --format or --pretty value only after =, so ../x is a path.
		{"git log --format ../x -5", true},
		{"/usr/bin/git log --pretty ../x", true},
	} {
		a := Action{Kind: "exec", Target: tc.command, Role: "implement", Work: root, Cwd: root}
		if got := ExecOutside(a); got != tc.person {
			t.Errorf("%q: outside=%v, want %v", tc.command, got, tc.person)
		}
		if tc.person {
			if d := Evaluate(p, LeadRules{}, mode.Ask, a); !PersonOnly(d) {
				t.Errorf("%q: %+v", tc.command, d)
			}
		}
	}
}

func TestExecOutsideSkipsEachProgramAndWrappers(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	for _, tc := range []struct {
		command string
		person  bool
	}{
		{"go test ./... >/dev/null && /bin/echo done", false},
		{"go test ./... >/dev/null || /bin/echo done", false},
		{"go test ./... >/dev/null; /bin/echo done", false},
		{"go test ./... >/dev/null | /bin/cat", false},
		{"ls && env MODE=test /bin/echo done", false},
		{"ls && MODE=test sudo nohup /bin/echo done", false},
		{"true; timeout 9 /bin/echo done", false},
		{"ls && env cp ~/.ssh/id_rsa k", true},
		{"true; timeout 9 tar czf x.tgz ~/notes", true},
		{"ls && MODE=test sudo nohup /bin/cp " + absoluteTestPath(root, "/other/key") + " k", true},
		{"ls && /bin/echo '" + absoluteTestPath(root, "/other/a") + "|" + absoluteTestPath(root, "/other/b") + "'", true},
	} {
		a := Action{Kind: "exec", Target: tc.command, Role: "implement", Work: root, Cwd: root}
		if got := ExecOutside(a); got != tc.person {
			t.Errorf("%q: outside=%v, want %v", tc.command, got, tc.person)
		}
		d := Evaluate(p, LeadRules{}, mode.Ask, a)
		if d.Kind != Ask || PersonOnly(d) != tc.person {
			t.Errorf("%q: %+v, person=%v", tc.command, d, tc.person)
		}
	}
}
