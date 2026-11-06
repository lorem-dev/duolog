package remembertest_test

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/app/remember/remembertest"
)

func TestNativeAllowContract(t *testing.T) {
	remembertest.NativeAllowContract(t, func(*testing.T) *remembertest.Fake {
		return &remembertest.Fake{Exact: "Bash(make generate_types)", Prefix: "Bash(ruff check:*)", MCP: "mcp__tracker__get_issue", MCPGlob: "no rule"}
	}, remembertest.Want{Exact: "Bash(make generate_types)", Prefix: "Bash(ruff check:*)", MCP: "mcp__tracker__get_issue", MCPGlob: "no rule"})
}
