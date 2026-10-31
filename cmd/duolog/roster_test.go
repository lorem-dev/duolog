package main

import (
	"os"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/testbin"
)

func TestRosterAliasesAndVersions(t *testing.T) {
	isolatedRouteConfig(t)
	t.Chdir(t.TempDir())
	binary := testbin.Install(t, "codex", testbin.Scenario{Stdout: `{"models":[{"slug":"gpt-99-sol","visibility":"list"}]}`})
	out, err := executeBusCommand(t, "roster")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "implement=sol") || strings.Contains(out, "gpt-") {
		t.Fatal(out)
	}
	if _, err := os.Stat(binary + ".args.json"); !os.IsNotExist(err) {
		t.Fatal("plain roster ran codex")
	}
	out, err = executeBusCommand(t, "roster", "--versions")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "gpt-99-sol") {
		t.Fatal(out)
	}
}
