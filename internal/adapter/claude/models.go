package claude

import (
	"context"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/route"
)

// Resolver resolves a Claude model name. A Claude alias is its own model: the
// CLI takes it as it is, so there is no catalog to read and no cache.
type Resolver struct{}

// Resolve gives a pinned claude- name as it is and any other name in its
// normal form (opus, sonnet, haiku).
func (Resolver) Resolve(_ context.Context, _ *config.Config, _ string, target config.Target) (string, error) {
	if strings.HasPrefix(target.Model, "claude-") {
		return target.Model, nil
	}
	return route.Normalize(target.Agent, target.Model), nil
}
