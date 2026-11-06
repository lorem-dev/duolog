package delegatetest

import (
	"context"
	"errors"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
)

// scripted is a resolver that gives a fixed answer.
type scripted struct {
	model string
	err   error
}

func (s scripted) Resolve(context.Context, *config.Config, string, config.Target) (string, error) {
	return s.model, s.err
}

// TestModelResolverContract runs the contract against a scripted fake.
func TestModelResolverContract(t *testing.T) {
	ModelResolverContract(t, func(_ *testing.T, c ModelResolverCase) (ModelSetup, bool) {
		switch c {
		case CaseAlias:
			return ModelSetup{Resolver: scripted{model: "opus"}, Cfg: &config.Config{}, Target: config.Target{Agent: "claude", Model: "opus"}, Want: "opus"}, true
		case CaseFamily:
			return ModelSetup{Resolver: scripted{model: "gpt-6.1-sol"}, Cfg: &config.Config{}, Target: config.Target{Agent: "codex", Model: "sol"}, Want: "gpt-6.1-sol"}, true
		}
		return ModelSetup{Resolver: scripted{err: errors.New("offline")}, Cfg: &config.Config{}, Target: config.Target{Agent: "codex", Model: "sol"}, WantErr: true}, true
	})
}
