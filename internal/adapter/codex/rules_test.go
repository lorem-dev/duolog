package codex_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"github.com/lorem-dev/duolog/internal/app/remember/remembertest"
)

func TestNativeAllowContract(t *testing.T) {
	remembertest.NativeAllowContract(t, func(*testing.T) codex.NativeAllow { return codex.NativeAllow{} }, remembertest.Want{
		Exact:   "no rule",
		Prefix:  `prefix_rule(pattern = ["ruff", "check"], decision = "allow"`,
		MCP:     "no rule",
		MCPGlob: "no rule",
	})
}

func TestNativeAllowWritesAPrefixRuleOnce(t *testing.T) {
	root := t.TempDir()
	for range 2 {
		if _, err := (codex.NativeAllow{}).Allow(root, "make generate_types *"); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(filepath.Join(root, ".codex", "rules", "duolog.rules"))
	if strings.Count(string(data), `prefix_rule(pattern = ["make", "generate_types"], decision = "allow"`) != 1 {
		t.Fatalf("codex rules: %s", data)
	}
}
