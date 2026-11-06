package delegatetest

import (
	"context"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
)

// ModelResolverCase names one case of ModelResolverContract.
type ModelResolverCase string

// The cases of ModelResolverContract.
const (
	// CaseAlias: a Claude alias resolves to itself.
	CaseAlias ModelResolverCase = "claude alias resolves to itself"
	// CaseFamily: a Codex family resolves to the newest visible version.
	CaseFamily ModelResolverCase = "codex family resolves to the newest visible version"
	// CaseFailing: a failing CLI is an error, and never the bare family.
	CaseFailing ModelResolverCase = "failing cli is an error"
)

// ModelResolver is the shape of a delegate.ModelResolver that the contract
// needs, named here so that an agent's tests need not import the app package.
type ModelResolver interface {
	Resolve(ctx context.Context, cfg *config.Config, cacheDir string, t config.Target) (string, error)
}

// ModelSetup is one case, ready to run: the resolver, the config and target to
// resolve, and the model it must give. WantErr says it must fail instead.
type ModelSetup struct {
	Resolver ModelResolver
	Cfg      *config.Config
	Target   config.Target
	Want     string
	WantErr  bool
}

// ModelResolverContract runs the cases that every ModelResolver meets. The
// factory returns the setup of a case, or false when the resolver's agent has
// no such case: Claude has no catalog to fail, Codex no alias that stands for
// itself.
func ModelResolverContract(t *testing.T, setup func(t *testing.T, c ModelResolverCase) (ModelSetup, bool)) {
	t.Helper()
	for _, c := range []ModelResolverCase{CaseAlias, CaseFamily, CaseFailing} {
		t.Run(string(c), func(t *testing.T) {
			s, ok := setup(t, c)
			if !ok {
				t.Skip("no such case for this agent")
			}
			got, err := s.Resolver.Resolve(context.Background(), s.Cfg, t.TempDir(), s.Target)
			switch {
			case s.WantErr && err == nil:
				t.Fatalf("resolved to %q, want an error", got)
			case s.WantErr && got == s.Target.Model:
				t.Fatalf("an error came with the bare family %q", got)
			case !s.WantErr && (err != nil || got != s.Want):
				t.Fatalf("got %q, %v; want %q", got, err, s.Want)
			}
		})
	}
}
