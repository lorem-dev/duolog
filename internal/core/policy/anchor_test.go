package policy

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// A lead rule with Under compares a target inside the work root put below
// Under, with the matcher, case folding and braces of every deny; an absolute
// Under makes the absolute target that an absolute selector compares.
func TestAnchoredLeadDeny(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct {
		name        string
		rule        config.Rule
		deny, allow []Action
	}{
		{
			name: "glob first segment",
			rule: config.Rule{Action: "read", Match: "**/private/**", Under: "module"},
			deny: []Action{
				{Kind: "read", Target: "private/data.txt"}, {Kind: "read", Target: "src/private/key"},
				{Kind: "exec", Target: "cat private/data.txt"},
			},
			allow: []Action{{Kind: "read", Target: "src/data.txt"}, {Kind: "read", Target: "privates/data.txt"}},
		},
		{
			name:  "file name glob",
			rule:  config.Rule{Action: "read", Match: "**/.env", Under: "module"},
			deny:  []Action{{Kind: "read", Target: ".env"}, {Kind: "read", Target: "config/.env"}},
			allow: []Action{{Kind: "read", Target: "env.txt"}},
		},
		{
			name:  "extension glob",
			rule:  config.Rule{Action: "write", Match: "**/*.key", Under: "module"},
			deny:  []Action{{Kind: "write", Target: "a.key"}, {Kind: "exec", Target: "echo x > certs/a.key"}},
			allow: []Action{{Kind: "write", Target: "a.keys"}},
		},
		{
			name:  "braces",
			rule:  config.Rule{Action: "write", Match: "{module/locked,other}/**", Under: "module"},
			deny:  []Action{{Kind: "write", Target: "locked/a.go"}},
			allow: []Action{{Kind: "write", Target: "other/a.go"}, {Kind: "write", Target: "src/a.go"}},
		},
		{
			name:  "case on every host",
			rule:  config.Rule{Action: "write", Match: "MODULE/locked/**", Under: "module"},
			deny:  []Action{{Kind: "write", Target: "locked/a.go"}, {Kind: "write", Target: "LOCKED/a.go"}},
			allow: []Action{{Kind: "write", Target: "src/a.go"}},
		},
		{
			name:  "nested anchor",
			rule:  config.Rule{Action: "write", Match: "module/nested/sealed/**", Under: "module/nested"},
			deny:  []Action{{Kind: "write", Target: "sealed/a.go"}},
			allow: []Action{{Kind: "write", Target: "nested/sealed/a.go"}},
		},
		{
			name: "absolute selector on another drive",
			rule: config.Rule{Action: "write", Match: "D:/trees/module/locked/**", Under: "D:/trees/module"},
			deny: []Action{
				{Kind: "write", Target: "locked/a.go"}, {Kind: "write", Target: "Locked/a.go"},
				{Kind: "write", Target: "D:/trees/module/locked/a.go"},
			},
			allow: []Action{{Kind: "write", Target: "src/a.go"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.rule.Decision = Deny
			lead := LeadRules{Deny: []config.Rule{tc.rule}}
			for _, a := range tc.deny {
				a.Role = "implement"
				check(t, p, lead, mode.Full, a, Deny)
			}
			for _, a := range tc.allow {
				a.Role = "implement"
				check(t, p, lead, mode.Full, a, Allow)
			}
		})
	}
}

// A target outside the work root is not put below a relative Under: the
// lead's files there are named by the rule's moved form.
func TestAnchoredTargetOutsideWorkRoot(t *testing.T) {
	for _, tc := range []struct{ under, target, want string }{
		{"module", "locked/a.go", "module/locked/a.go"},
		{"module", ".", "module"},
		{"module", "../secrets/key", "../secrets/key"},
		{"module", "/etc/passwd", "/etc/passwd"},
		{"module", "~/x", "~/x"},
		{"", "locked/a.go", "locked/a.go"},
		{"/trees/module", "../x", "/trees/x"},
		{"D:/trees/module", "locked/a.go", "D:/trees/module/locked/a.go"},
	} {
		got := anchoredTarget(config.Rule{Under: tc.under}, tc.target)
		if got != tc.want && got != toLowerASCII(tc.want) {
			t.Errorf("anchoredTarget(%q, %q) = %q, want %q", tc.under, tc.target, got, tc.want)
		}
	}
}

func toLowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}
