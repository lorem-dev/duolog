package rex

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/app/peer/peertest"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// contractRex is the world of peertest.PaneContract as rex answers it.
func contractRex() map[string]testbin.Scenario {
	notFound := testbin.Scenario{Exit: 3, Stderr: `block target "block:missing" not found`}
	return map[string]testbin.Scenario{
		"new":           {Stdout: `{"session_id":"session:new","block_ids":["block:new"]}`},
		"window new":    {Stdout: `{"block_ids":["block:tab"]}`},
		"block inspect": {Stdout: `{"block":{"id":"block:new"}}`},
		"block inspect -s session:new --json block:missing": notFound,
		"wait":    {Exit: 1, Stderr: "Error: wait timed out after 500ms"},
		"capture": {Stdout: "a\n\nb\nc\n"},
		"capture -s session:new -b block:missing": notFound,
		"send":        {},
		"send-key":    {},
		"block close": {},
		"block close -s session:new block:missing": notFound,
		"-C":             {},
		"session list":   {Stdout: `{"sessions":[{"session_id":"sess","label":"lead"}]}`},
		"session rename": {},
		"kill":           {},
		"client ls":      {Stdout: `{"clients":[{"client_id":"client:app","info":{"kind":"app"},"session_ids":["sess"]}]}`},
	}
}

func TestPaneContract(t *testing.T) {
	peertest.PaneContract(t, func(t *testing.T) peertest.Pane {
		c := install(t, testbin.Scenario{Exit: 99, Stderr: "unexpected rex command", Responses: contractRex()})
		return Pane{Client: c}
	})
}

// A zero Pane finds rex and the lead's session as Detect does.
func TestPaneDetectsRex(t *testing.T) {
	bin := testbin.Install(t, "rex", testbin.Scenario{})
	t.Setenv("REX_BIN_DIR", "")
	t.Setenv("REX_SESSION", "sess")
	if session, ok := (Pane{}).Lead(); !ok || session != "sess" {
		t.Fatalf("Lead() = %q, %v with rex at %s", session, ok, bin)
	}
	t.Setenv("REX_SESSION", "")
	if _, ok := (Pane{}).Lead(); ok {
		t.Fatal("Lead() found a session outside Rex")
	}
}
