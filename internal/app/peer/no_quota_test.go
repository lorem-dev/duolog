package peer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/account"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
)

// An agent with no Quota fails closed. Its account is never read, so
// paidrules.Evaluate makes its pane paid with read:failed. Though
// paid_runs.codex allows paid runs with a cap, no paid pass is tried: the pane
// is refused, and nothing is held.
func TestNoQuotaNoPaidPass(t *testing.T) {
	r := newRig(t, baseScript())
	r.paidCodex()
	read := r.gate.Cache.Read
	r.gate.Cache.Read = func(ctx context.Context, p corelaunch.Process) (account.Snapshot, error) {
		if p.Agent == "codex" {
			return account.Snapshot{}, errors.New("no quota reader")
		}
		return read(ctx, p)
	}
	r.useDeps(func(d *Deps) { d.Quota = map[string]bool{"claude": true} })

	_, err := Open(r.ctx, "implement")
	if err == nil {
		t.Fatal("a pane opened for an agent without a quota reader")
	}
	if !strings.Contains(err.Error(), "read:failed") {
		t.Fatalf("the refusal does not name the failed read: %v", err)
	}
	if strings.Contains(err.Error(), "daily cap") || strings.Contains(err.Error(), "paid variant") {
		t.Fatalf("a paid pass was tried for an agent without a quota reader: %v", err)
	}
	r.wantHeld(0, 0)
}
