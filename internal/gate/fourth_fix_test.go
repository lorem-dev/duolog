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
