package claude_test

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/app/delegate/delegatetest"
	"github.com/lorem-dev/duolog/internal/core/config"
)

// TestModelResolverContract runs the ModelResolver contract on Claude's Resolver.
func TestModelResolverContract(t *testing.T) {
	delegatetest.ModelResolverContract(t, func(_ *testing.T, c delegatetest.ModelResolverCase) (delegatetest.ModelSetup, bool) {
		if c != delegatetest.CaseAlias {
			return delegatetest.ModelSetup{}, false
		}
		return delegatetest.ModelSetup{Resolver: claude.Resolver{}, Cfg: &config.Config{}, Target: config.Target{Agent: "claude", Model: "opus"}, Want: "opus"}, true
	})
}
