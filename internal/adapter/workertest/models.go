package workertest

import (
	"context"
	"fmt"

	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/route"
)

// Models resolves the model names of both agents with the real resolvers. List,
// when set, supplies the Codex catalog instead of the codex program.
type Models struct {
	List func(context.Context) ([]route.Entry, string, error)
}

// Resolve gives the concrete model of target through its agent's resolver.
func (m Models) Resolve(ctx context.Context, cfg *config.Config, cacheDir string, target config.Target) (string, error) {
	switch target.Agent {
	case "claude":
		return claude.Resolver{}.Resolve(ctx, cfg, cacheDir, target)
	case "codex":
		return codex.Resolver{List: m.List}.Resolve(ctx, cfg, cacheDir, target)
	}
	return "", fmt.Errorf("unknown agent %q", target.Agent)
}
