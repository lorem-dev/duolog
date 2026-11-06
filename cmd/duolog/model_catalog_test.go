package main

import (
	"encoding/json"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// Fake catalogs use the configured price versions instead of pinning model
// versions in tests. Production code still resolves each requested alias.
func testModelCatalog(t *testing.T, aliases ...string) string {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var entries []route.Entry
	for _, alias := range aliases {
		entries = append(entries, route.Entry{Slug: cfg.Models[alias].PricedFor, Visibility: "list"})
	}
	data, err := json.Marshal(struct {
		Models []route.Entry `json:"models"`
	}{entries})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func withTestModelCatalog(t *testing.T, scenario testbin.Scenario, aliases ...string) testbin.Scenario {
	t.Helper()
	if scenario.Responses == nil {
		scenario.Responses = map[string]testbin.Scenario{}
	}
	scenario.Responses["--version"] = testbin.Scenario{Stdout: "test-codex"}
	scenario.Responses["debug models"] = testbin.Scenario{Stdout: testModelCatalog(t, aliases...)}
	return scenario
}
