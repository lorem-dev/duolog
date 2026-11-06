// Package remembertest holds the fake NativeAllow and the contract that every
// NativeAllow passes.
package remembertest

import (
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/remember"
)

// Fake records each call and returns the scripted results.
type Fake struct {
	// Exact, Prefix, MCP and MCPGlob are what Allow returns for an exact
	// command, Allow for a prefix, AllowMCP for one tool and AllowMCP for a glob.
	Exact, Prefix, MCP, MCPGlob string
	// Err is returned by every call when it is set.
	Err error

	// Calls lists the calls in order: "allow <match>" and "mcp <server>/<tool>".
	Calls []string
}

var _ remember.NativeAllow = (*Fake)(nil)

// Allow records the call and returns Prefix for a match that ends in " *",
// Exact for any other.
func (f *Fake) Allow(_, match string) (string, error) {
	f.Calls = append(f.Calls, "allow "+match)
	if f.Err != nil {
		return "", f.Err
	}
	if strings.HasSuffix(match, " *") {
		return f.Prefix, nil
	}
	return f.Exact, nil
}

// AllowMCP records the call and returns MCPGlob for a tool with a glob, MCP
// for any other.
func (f *Fake) AllowMCP(_, server, tool string) (string, error) {
	f.Calls = append(f.Calls, "mcp "+server+"/"+tool)
	if f.Err != nil {
		return "", f.Err
	}
	if strings.ContainsAny(tool, "*?[{") {
		return f.MCPGlob, nil
	}
	return f.MCP, nil
}

// Want holds, for one agent, a part of what each case of the contract returns.
type Want struct {
	// Exact is part of the result for the exact command "make generate_types".
	Exact string
	// Prefix is part of the result for the prefix "ruff check *".
	Prefix string
	// MCP is part of the result for the tool tracker/get_issue.
	MCP string
	// MCPGlob is part of the result for the glob tracker/get_*.
	MCPGlob string
}

// Port is remember.NativeAllow, written out so that the tests of an adapter
// can pass their type without importing the app package.
type Port interface {
	Allow(root, match string) (string, error)
	AllowMCP(root, server, tool string) (string, error)
}

var _ Port = remember.NativeAllow(nil)

// NativeAllowContract runs the cases that every NativeAllow passes. Each case
// runs in its own project folder.
func NativeAllowContract[P Port](t *testing.T, newPort func(t *testing.T) P, want Want) {
	t.Run("an exact command", func(t *testing.T) {
		contains(t, "Allow", must(t, func() (string, error) { return newPort(t).Allow(t.TempDir(), "make generate_types") }), want.Exact)
	})
	t.Run("a prefix", func(t *testing.T) {
		contains(t, "Allow", must(t, func() (string, error) { return newPort(t).Allow(t.TempDir(), "ruff check *") }), want.Prefix)
	})
	t.Run("one MCP tool", func(t *testing.T) {
		contains(t, "AllowMCP", must(t, func() (string, error) { return newPort(t).AllowMCP(t.TempDir(), "tracker", "get_issue") }), want.MCP)
	})
	t.Run("a glob of MCP tools", func(t *testing.T) {
		contains(t, "AllowMCP", must(t, func() (string, error) { return newPort(t).AllowMCP(t.TempDir(), "tracker", "get_*") }), want.MCPGlob)
	})
}

func must(t *testing.T, call func() (string, error)) string {
	t.Helper()
	got, err := call()
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func contains(t *testing.T, method, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("%s returned %q, want it to contain %q", method, got, want)
	}
}
