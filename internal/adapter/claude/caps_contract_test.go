package claude_test

import (
	"slices"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/app/delegate/delegatetest"
	"github.com/lorem-dev/duolog/internal/core/caps"
)

func TestCapabilitiesContract(t *testing.T) {
	delegatetest.CapabilitiesContract(t, "claude", func(*testing.T) delegatetest.CapsPort { return claude.Caps{} })
	// The refusals only a Claude worker gives, word for word.
	t.Run("claude refusal reasons word for word", func(t *testing.T) {
		root := t.TempDir()
		for want, s := range map[string]caps.Server{
			"it sets a working folder, which Claude cannot do":               {Name: "svc", Agent: "codex", Transport: "stdio", Command: "x", Cwd: "/elsewhere"},
			"its command, args or url contain ${, which Claude would expand": {Name: "svc", Agent: "codex", Transport: "stdio", Command: "x", Args: []string{"${T}"}},
		} {
			if got := (claude.Caps{}).Refusal(root, s, nil); got != want {
				t.Errorf("Refusal = %q, want %q", got, want)
			}
		}
	})
	// Claude names a tool mcp__<server>__<tool>, each part folded by
	// caps.ClaudeName.
	t.Run("tool names folded by ClaudeName", func(t *testing.T) {
		s := caps.Server{Name: "my.wiki", Agent: "codex", Transport: "stdio", Command: "w", DisabledTools: []string{"drop table"}}
		m, failed := (claude.Caps{}).Import(t.TempDir(), map[string]caps.Server{"my.wiki": s}, map[string][]string{"my.wiki": {"list.all"}}, nil)
		if m == nil || len(failed) != 0 {
			t.Fatal(m, failed)
		}
		if !slices.Equal(m.AllowedTools, []string{"mcp__my_wiki__list_all"}) || !slices.Equal(m.DisallowedTools, []string{"mcp__my_wiki__drop_table"}) {
			t.Fatal(m.AllowedTools, m.DisallowedTools)
		}
		_, allowed := (claude.Caps{}).Limit("", []string{"bad name"}, map[string][]string{"bad name": {"x"}}, nil)
		if !slices.Equal(allowed, []string{"mcp__bad_name__x"}) {
			t.Fatal(allowed)
		}
	})
}
