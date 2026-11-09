package paidruns

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/core/launch"
)

// A pane opened in Codex workspace-write has the Go cache variables; a send to
// it is built without the launch words that add them. Both are one route.
func TestRouteKeyIgnoresGoCacheVariables(t *testing.T) {
	open := launch.Process{Agent: "codex", Shape: launch.Peer, Variant: launch.Free,
		Env: []string{"HOME=/h", "GOCACHE=/t/go-build", "GOLANGCI_LINT_CACHE=/t/lint"}}
	send := launch.Process{Agent: "codex", Shape: launch.Peer, Variant: launch.Free, Env: []string{"HOME=/h"}}
	if RouteKey(open) != RouteKey(send) {
		t.Fatal("the Go cache variables moved the route key")
	}
	other := send
	other.Env = []string{"HOME=/h", "OTHER=1"}
	if RouteKey(other) == RouteKey(send) {
		t.Fatal("another variable did not move the route key")
	}
}
