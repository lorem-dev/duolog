package codex_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"github.com/lorem-dev/duolog/internal/app/delegate/delegatetest"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/route"
)

// TestModelResolverContract runs the ModelResolver contract on Codex's Resolver
// with the recorded catalog of `codex debug models`.
func TestModelResolverContract(t *testing.T) {
	data, err := os.ReadFile("../../../scripts/bench/fixtures/models/codex-debug-models.trimmed.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Models []route.Entry `json:"models"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Models: map[string]config.Model{"sol": {Agent: "codex", Family: "sol"}}}
	target := config.Target{Agent: "codex", Model: "sol"}
	delegatetest.ModelResolverContract(t, func(_ *testing.T, c delegatetest.ModelResolverCase) (delegatetest.ModelSetup, bool) {
		switch c {
		case delegatetest.CaseFamily:
			list := func(context.Context) ([]route.Entry, string, error) { return catalog.Models, "fixture", nil }
			return delegatetest.ModelSetup{Resolver: codex.Resolver{List: list}, Cfg: cfg, Target: target, Want: "gpt-6.1-sol"}, true
		case delegatetest.CaseFailing:
			list := func(context.Context) ([]route.Entry, string, error) { return nil, "", errors.New("offline") }
			return delegatetest.ModelSetup{Resolver: codex.Resolver{List: list}, Cfg: cfg, Target: target, WantErr: true}, true
		}
		return delegatetest.ModelSetup{}, false
	})
}
