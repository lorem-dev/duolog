package wiring

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"github.com/lorem-dev/duolog/internal/app/delegate"
)

// Codex gives a linked worktree the trust of the lead's checkout; Claude needs
// no hook approval, and its folder trust is not duolog's to write here.
func TestOnlyCodexAdoptsWorktrees(t *testing.T) {
	var codexTrust any = codex.Trust{}
	if _, ok := codexTrust.(delegate.WorktreeTrust); !ok {
		t.Fatal("codex.Trust does not adopt worktrees")
	}
	var claudeTrust any = claude.Trust{}
	if _, ok := claudeTrust.(delegate.WorktreeTrust); ok {
		t.Fatal("claude.Trust adopts worktrees")
	}
	if PeerDeps(delegate.Deps{}).AdoptWorktree == nil {
		t.Fatal("peer panes do not adopt worktrees")
	}
}
