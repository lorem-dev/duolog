package main

import (
	"os"
	"strings"
	"testing"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/testbin"
)

func TestRosterAliasesAndVersions(t *testing.T) {
	isolatedRouteConfig(t)
	routeLead(t)
	cfg, err := configfile.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	modelID := cfg.Models["sol"].PricedFor
	binary := testbin.Install(t, "codex", withTestModelCatalog(t, testbin.Scenario{}, "sol"))
	out, err := executeBusCommand(t, "roster")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "implement=sol") || strings.Contains(out, modelID) {
		t.Fatal(out)
	}
	if _, err := os.Stat(binary + ".args.json"); !os.IsNotExist(err) {
		t.Fatal("plain roster ran codex")
	}
	out, err = executeBusCommand(t, "roster", "--versions")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, modelID) {
		t.Fatal(out)
	}
}
