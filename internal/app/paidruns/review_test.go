package paidruns

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/quota"
)

func TestRouteKeyPinsPrivateRouteValues(t *testing.T) {
	// Kills hashing only environment names and ignoring route settings.
	original := launch.Process{Agent: "claude", Shape: launch.Peer, Variant: launch.Paid, Env: []string{"CLAUDE_CONFIG_DIR=/account-a", "ANTHROPIC_API_KEY=example-a"}, Settings: `{"apiKeyHelper":"helper-a"}`}
	for _, change := range []func(*launch.Process){
		func(p *launch.Process) {
			p.Env = []string{"CLAUDE_CONFIG_DIR=/account-b", "ANTHROPIC_API_KEY=example-a"}
		},
		func(p *launch.Process) {
			p.Env = []string{"CLAUDE_CONFIG_DIR=/account-a", "ANTHROPIC_API_KEY=example-b"}
		},
		func(p *launch.Process) { p.Settings = `{"apiKeyHelper":"helper-b"}` },
	} {
		changed := original
		change(&changed)
		if RouteKey(original) == RouteKey(changed) {
			t.Error("route value changed without a new identity")
		}
	}
}

func TestPeerSendRejectsChangedAccount(t *testing.T) {
	// Kills admitting a pane task against the quota of a different account.
	w := newGateWorld(t)
	if _, err := w.store.Identity("claude", quota.IdentityAccount, []byte("account-a")); err != nil {
		t.Fatal(err)
	}
	open := w.acquire(w.peerOpenRequest("claude", launch.Free, 100))
	if _, err := w.store.Identity("claude", quota.IdentityAccount, []byte("account-b")); err != nil {
		t.Fatal(err)
	}
	w.refused(w.peerSend("claude", launch.Free, 101, open.Reservation.ID), ReasonPaneRoute)
}
