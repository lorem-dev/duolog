package gate

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/mode"
)

func TestReadDenyProbesMatchGlobLastSegment(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	for _, glob := range []string{"config/.env*", "*/.env", "src/*.key", "config/.env*/**"} {
		for _, kind := range []string{"exec", "read"} {
			a := Action{Kind: kind, Role: "implement", Work: root, Cwd: root, State: root}
			if kind == "exec" {
				a.Target = "rg -g '" + glob + "' x ."
			} else {
				a.Target, a.Search, a.Glob = ".", true, glob
			}
			d := Evaluate(p, LeadRules{}, mode.Full, a)
			if d.Kind != Ask || d.Rule != "read command" || PersonOnly(d) {
				t.Errorf("%s %q: %+v", kind, glob, d)
			}
		}
	}
}

func TestExecOutsideRetainsDroppedWrappedParts(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	for _, command := range []string{"ls && env cp ~/.ssh/id_rsa k", "true; timeout 9 tar czf x.tgz ~/notes"} {
		a := Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root}
		if !ExecOutside(a) {
			t.Errorf("%q: missing outside operand", command)
		}
		if d := Evaluate(p, LeadRules{}, mode.Ask, a); d.Kind != Ask || !PersonOnly(d) {
			t.Errorf("%q: %+v", command, d)
		}
	}
}

func TestExecOutsideExemptsOnlyStreamDevices(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	for _, tc := range []struct {
		target string
		person bool
	}{
		{"/dev/null", false}, {"/dev/stdin", false}, {"/dev/stdout", false},
		{"/dev/stderr", false}, {"/dev/fd/3", false},
		{"/dev/tcp/example.invalid/443", true}, {"/dev/udp/example.invalid/53", true},
		{"/dev/zero", true}, {"/dev", true}, {"/dev/fd-other/3", true},
	} {
		// The device spellings themselves remain checked without a native cwd.
		if got := ExecOutside(Action{Kind: "exec", Target: "make test >" + tc.target}); got != tc.person {
			t.Errorf("device %q: outside=%v, want %v", tc.target, got, tc.person)
		}
		if tc.person {
			tc.target = absoluteTestPath(root, tc.target)
		}
		a := Action{Kind: "exec", Target: "make test >" + tc.target, Role: "implement", Work: root, Cwd: root}
		if got := ExecOutside(a); got != tc.person {
			t.Errorf("%q: outside=%v, want %v", a.Target, got, tc.person)
		}
		d := Evaluate(p, LeadRules{}, mode.Ask, a)
		if d.Kind != Ask || PersonOnly(d) != tc.person {
			t.Errorf("%q: %+v, person=%v", a.Target, d, tc.person)
		}
	}
}
