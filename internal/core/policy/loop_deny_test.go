package policy

import (
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// A loop header, a case line and a closing done hide nothing from a deny: the
// write rule, a lead write deny and the state read rule judge what the line
// can do, in every mode, as in 1.4.0.
func TestLoopAndCaseLinesKeepDenies(t *testing.T) {
	p := policy(t)
	commands := []string{
		`case x in a ) cp y .git/hooks/pre-commit;; esac`,
		`case x in a ) cat .duolog/bus.ndjson;; esac`,
		`case x in a | b ) cp y .git/hooks/pre-commit;; esac`,
		`case x in a) ls;; b ) cp y .git/hooks/pre-commit;; esac`,
		`case x in a b c cp y .git/hooks/pre-commit;; esac`,
		`for f in .git/hooks/pre-commit; do cp x "$f"; done`,
		`select f in .git/hooks/pre-commit; do cp x "$f"; done`,
		`for f in .git/hooks/pre-commit; do cp x $f; done`,
		`for f in .git/hooks/pre-commit; do nohup cp x $f; done`,
		`for f in .duolog/bus.ndjson; do head "$f"; done`,
		`select f in .duolog/bus.ndjson; do head "$f"; done`,
		`while read l; do echo $l; done < .duolog/bus.ndjson`,
		`while read l; do echo $l; done <.duolog/bus.ndjson`,
		`while read l; do echo $l; done 0< .duolog/bus.ndjson`,
		`done < .duolog/bus.ndjson`,
		`fi <.duolog/bus.ndjson`,
		`while read l; do echo $l; done < .env`,
		`done .git/hooks/pre-commit`,
		// Finding 6: >& with a file name writes it, and a wrapper word does
		// not hide the command after it.
		`echo x >& .git/hooks/pre-commit`,
		`echo x >&.git/hooks/pre-commit`,
		`command cat .duolog/bus.ndjson`,
		`nohup cat .duolog/bus.ndjson`,
		`env cat .duolog/bus.ndjson`,
		`env A=b cat .duolog/bus.ndjson`,
		`command -p cat .duolog/bus.ndjson`,
		`exec cat .duolog/bus.ndjson`,
		`timeout 5 cat .duolog/bus.ndjson`,
		`command time cat .duolog/bus.ndjson`,
	}
	for _, command := range commands {
		for _, m := range []mode.Mode{mode.Full, mode.Ask} {
			d := Evaluate(p, LeadRules{}, m, Action{Kind: "exec", Target: command, Role: "implement"})
			if d.Kind != Deny {
				t.Errorf("%s (%s): %+v, want deny", command, m, d)
			}
		}
	}
	lead := LeadRules{Deny: []config.Rule{{Action: "write", Match: "src/**", Decision: Deny}}}
	for _, command := range []string{
		`for f in src/a.go; do cp x "$f"; done`,
		`case x in a ) cp y src/a.go;; esac`,
		`echo x >& src/a.go`,
	} {
		for _, m := range []mode.Mode{mode.Full, mode.Ask} {
			d := Evaluate(p, lead, m, Action{Kind: "exec", Target: command, Role: "implement"})
			if d.Kind != Deny {
				t.Errorf("lead deny, %s (%s): %+v, want deny", command, m, d)
			}
		}
	}
}

// What e8466de wanted stays: a listing loop whose commands cannot write is not
// judged by the write rule, and a readable state file stays readable.
func TestListingLoopIsStillNotAWrite(t *testing.T) {
	p := policy(t)
	// The .*/ glob names the state folder, so a read check may deny this one;
	// the write rule must not.
	loop := `ls -d .*/; for d in assets cmd docs .duolog .claude; do echo "== $d"; ls $d 2>/dev/null | head -15; done`
	if d := Evaluate(p, LeadRules{}, mode.Full, Action{Kind: "exec", Target: loop, Role: "implement"}); strings.HasPrefix(d.Rule, "write") {
		t.Errorf("%s: judged by the write rule: %+v", loop, d)
	}
	for _, command := range []string{
		`for d in assets cmd docs .duolog .claude; do echo "== $d"; ls $d 2>/dev/null | head -15; done`,
		`for d in .duolog .git; do ls $d; done`,
		`select d in .duolog .git; do ls $d; done`,
		`case x in a ) ls .git;; esac`,
		`while read l; do echo $l; done < .duolog/config.yaml`,
		`command ls .duolog`,
	} {
		d := Evaluate(p, LeadRules{}, mode.Full, Action{Kind: "exec", Target: command, Role: "implement"})
		if d.Kind == Deny || strings.HasPrefix(d.Rule, "write") {
			t.Errorf("%s: %+v, want no deny", command, d)
		}
	}
}

func TestInputTargets(t *testing.T) {
	for _, tc := range []struct {
		part string
		want []string
	}{
		{"done < a", []string{"a"}},
		{"done <a 0<b", []string{"a", "b"}},
		{"cat <a<b", []string{"a", "b"}},
		{"cat <> a", []string{"a"}},
		{"cat <&3 <&-", nil},
		{"cat << EOF", nil},
		{"cat <<< text", nil},
		{"cat <<-EOF", nil},
		{"cat <'a'", []string{"a"}},
		{"cat 2>a <b", []string{"b"}},
	} {
		if got := inputTargets(tc.part); strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("%s: %q, want %q", tc.part, got, tc.want)
		}
	}
}
