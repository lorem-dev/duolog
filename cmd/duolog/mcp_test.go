package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/wiring/wiringtest"
)

const mcpListInput = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
`

func runMCPCommand(t *testing.T, dir string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DUOLOG_PEER", "")
	t.Setenv("DUOLOG_DEPTH", "")
	t.Chdir(dir)
	cmd := newRootCommand(newCmdDeps(wiringtest.Cmd(t)))
	var out bytes.Buffer
	cmd.SetIn(strings.NewReader(mcpListInput))
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"mcp"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestMCPCommandListsNoToolsOutsideProject(t *testing.T) {
	out := runMCPCommand(t, t.TempDir())
	if !strings.Contains(out, `"tools":[]`) {
		t.Fatalf("output = %s", out)
	}
}

func TestMCPCommandListsToolsInProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".duolog"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".duolog", "config.yaml"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runMCPCommand(t, dir)
	for _, name := range []string{`"name":"delegate"`, `"name":"peer"`, `"name":"answer_ask"`, `"name":"status"`} {
		if !strings.Contains(out, name) {
			t.Fatalf("output lacks %s: %s", name, out)
		}
	}
}
