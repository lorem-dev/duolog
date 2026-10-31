package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/testbin"
)

func TestCapsDiffAndJSON(t *testing.T) {
	isolatedRouteConfig(t)
	root := t.TempDir()
	t.Chdir(root)
	claude := testbin.Install(t, "claude", testbin.Scenario{})
	codex := testbin.Install(t, "codex", testbin.Scenario{Responses: map[string]testbin.Scenario{"plugin list --json": {Stdout: `{"installed":[]}`}}})
	t.Setenv("PATH", filepath.Dir(claude)+string(os.PathListSeparator)+filepath.Dir(codex))
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers":{"only-claude":{"env":{"K":"secret-value"}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := executeBusCommand(t, "caps", "--diff")
	if err != nil || strings.TrimSpace(out) != "claude mcp only-claude" {
		t.Fatal(out, err)
	}
	out, err = executeBusCommand(t, "caps", "--json")
	var inv []struct {
		Agent string
		Items []struct{ Kind, Name string }
	}
	if err != nil || json.Unmarshal([]byte(out), &inv) != nil || len(inv) != 2 || strings.Contains(out, "secret-value") {
		t.Fatal(out, err)
	}
}

func TestDelegateNeedsExitCodeIsNotFailure(t *testing.T) {
	if exitCode(&needsExit{}) == exitCode(os.ErrNotExist) || exitCode(&needsExit{}) == 0 {
		t.Fatal("needs exit code must differ from failure and success")
	}
}
