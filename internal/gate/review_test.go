package gate

import (
	"github.com/lorem-dev/duolog/internal/mode"
	"strings"
	"testing"
)

func TestReviewWorktreeGitFileWriteDenied(t *testing.T) {
	p := policy(t)
	explicit := false
	for _, rule := range p.Rules {
		if rule.Action == "write" && rule.Decision == Deny && (rule.Match == "**/.git" || strings.Contains(rule.Match, "**/.git,") || strings.Contains(rule.Match, "**/.git}")) {
			explicit = true
		}
	}
	if !explicit {
		t.Error("default policy does not explicitly protect the .git file")
	}
	for _, path := range []string{".git", "nested/.git"} {
		check(t, p, LeadRules{}, mode.Full, Action{Kind: "write", Target: path, Role: "implement"}, Deny)
	}
}
