package claude_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/app/remember/remembertest"
)

func TestNativeAllowContract(t *testing.T) {
	remembertest.NativeAllowContract(t, func(*testing.T) claude.NativeAllow { return claude.NativeAllow{} }, remembertest.Want{
		Exact:   "Bash(make generate_types)",
		Prefix:  "Bash(ruff check:*)",
		MCP:     "mcp__tracker__get_issue",
		MCPGlob: "no rule",
	})
}

func TestNativeAllowKeepsOtherSettings(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"permissions":{"allow":["Bash(ls:*)"]},"model":"opus"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := (claude.NativeAllow{}).Allow(root, ".venv/bin/ruff check *"); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(path)
	for _, want := range []string{`"Bash(ls:*)"`, `"Bash(.venv/bin/ruff check:*)"`, `"model": "opus"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("settings lost or missed %s: %s", want, data)
		}
	}
	if n := strings.Count(string(data), "ruff check"); n != 1 {
		t.Fatalf("the rule was written %d times: %s", n, data)
	}
}
