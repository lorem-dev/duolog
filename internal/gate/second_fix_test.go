package gate

import (
	"encoding/json"
	"path"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
)

func TestGoOverrideUsesReadDenyProbes(t *testing.T) {
	if ok, err := path.Match("*.go", ".env.go"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	d := Evaluate(policy(t), LeadRules{}, mode.Edits, Action{Kind: "read", Target: ".env.go", Role: "implement"})
	if d.Kind != Deny {
		t.Fatal(d)
	}
	p := policy(t)
	root := t.TempDir()
	for _, tc := range []struct{ glob, want string }{
		{"*.env*", Ask}, {"*.key", Ask}, {"secrets", Ask}, {"**/*.pem", Ask},
		{"{*.go,*.key}", Ask}, {"*.go", Allow}, {"**/*.go", Allow},
		{"cmd/**", Allow}, {"*test*", Allow}, {"!*.key", Allow},
	} {
		command := "rg -g '" + tc.glob + "' x ."
		a := Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root, State: root}
		got := Evaluate(p, LeadRules{}, mode.Full, a)
		if got.Kind != tc.want || PersonOnly(got) || tc.want == Ask && got.Rule != "read command" {
			t.Errorf("%q: %+v, want %s", command, got, tc.want)
		}
	}
}

func TestReadDenyProbesCoverGrepAndProjectRules(t *testing.T) {
	p := policy(t)
	p.Rules = append([]config.Rule{{Action: "read", Match: "{**/*.secret,**/private/**}", Decision: Deny}}, p.Rules...)
	root := t.TempDir()
	for _, glob := range []string{"*.key", "*.env*", "secrets", "*.secret", "private"} {
		data, _ := json.Marshal(map[string]any{"tool_name": "Grep", "tool_input": map[string]string{"path": ".", "pattern": "x", "glob": glob}})
		a, err := Normalize("claude", data)
		if err != nil {
			t.Fatal(err)
		}
		a.Work, a.Cwd, a.State, a.Role = root, root, root, "implement"
		d := Evaluate(p, LeadRules{}, mode.Full, a)
		if d.Kind != Ask || d.Rule != "read command" || PersonOnly(d) {
			t.Errorf("Grep %q: %+v", glob, d)
		}
		a = Action{Kind: "exec", Target: "rg --glob='" + glob + "' x .", Role: "implement", Work: root, Cwd: root, State: root}
		if d := Evaluate(p, LeadRules{}, mode.Full, a); d.Kind != Ask || d.Rule != "read command" || PersonOnly(d) {
			t.Errorf("rg %q: %+v", glob, d)
		}
	}
}

func TestReadDenyProbesCheckAllRgGlobForms(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	for _, command := range []string{
		"rg . -g *.key", "rg . -g *.go", "rg --iglob='*.KEY' x .",
		"rg --glob '*.key' x .", "rg -g'*.key' x .",
		"rg -g '*.go' -g '*.key' x .",
	} {
		d := Evaluate(p, LeadRules{}, mode.Full, Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root, State: root})
		if d.Kind != Ask || d.Rule != "read command" || PersonOnly(d) {
			t.Errorf("%q: %+v", command, d)
		}
	}
}

func TestShellWildcardsDoNotNameHiddenState(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	for _, command := range []string{"grep x src/*", "ls cmd/*", "wc -l internal/gate/*", "cat secrets/*"} {
		d := Evaluate(p, LeadRules{}, mode.Edits, Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root, State: root})
		if d.Kind != Ask || d.Rule == RuleState {
			t.Errorf("%q: %+v", command, d)
		}
	}
}

func TestOverrideGlobsUseLastSegmentForState(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	for _, command := range []string{"rg -g '**/*.go' x .", "rg -g 'cmd/**' x .", "rg -g '*.ndjson' x .", "rg -g '!*o*' x ."} {
		d := Evaluate(p, LeadRules{}, mode.Edits, Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root, State: root})
		if d.Kind != Allow {
			t.Errorf("%q: %+v", command, d)
		}
	}
	for _, glob := range []string{".duolog", ".d*", ".[d]uolog", ".*", "{.DUOLOG,*.go}", "*", "**", "*o*", ".d*/**"} {
		if !stateOverride([]string{glob}) {
			t.Error(glob)
		}
	}
	data := []byte(`{"tool_name":"Grep","tool_input":{"path":".","pattern":"x","glob":"**/*.ts"}}`)
	a, err := Normalize("claude", data)
	if err != nil {
		t.Fatal(err)
	}
	a.Work, a.Cwd, a.State, a.Role = root, root, root, "implement"
	if d := Evaluate(p, LeadRules{}, mode.Edits, a); d.Kind == Deny && d.Rule == RuleState {
		t.Fatal(d)
	}
}

func TestExecPersonOnlyIgnoresStreamsAndNonPaths(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	for _, tc := range []struct {
		command string
		person  bool
	}{
		{"go test ./... >/dev/null", false},
		{"make test > /dev/null 2>&1", false},
		{"/usr/bin/env go test", false},
		{"/bin/ls " + absoluteTestPath(root, "/dev/zero"), true},
		{"git log --grep=/x", false},
		{"cp ~/.ssh/k k", true},
		{"tar czf x.tgz ~/notes", true},
		{"make test > " + absoluteTestPath(root, "/other/out"), true},
	} {
		a := Action{Kind: "exec", Target: tc.command, Role: "implement", Work: root, Cwd: root}
		if got := ExecOutside(a); got != tc.person {
			t.Errorf("%q: outside=%v, want %v", tc.command, got, tc.person)
		}
		d := Evaluate(p, LeadRules{}, mode.Ask, a)
		if tc.person {
			if !PersonOnly(d) {
				t.Errorf("%q: %+v", tc.command, d)
			}
		} else if PersonOnly(d) {
			t.Errorf("%q: %+v", tc.command, d)
		}
	}
}
