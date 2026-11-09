package codex

import (
	"slices"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/peer/peertest"
	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/screen"
)

func TestPaneAgentContract(t *testing.T) {
	peertest.PaneAgentContract(t, func(*testing.T) peertest.PaneAgent { return Pane{} })
}

// A plugin offer screen gives an offer for Claude and none for Codex.
func TestPaneOfferContract(t *testing.T) {
	s := screen.Screen{State: screen.Question, Options: []screen.Option{{Number: 1, Label: "Yes, install gopls-lsp"}},
		Text: "Plugin: gopls-lsp\nWould you like to install this LSP plugin?\n"}
	peertest.PaneOfferContract(t, func(*testing.T) peertest.PaneAgent { return Pane{} }, s, false, "")
}

func TestPaneArgvPinsNotificationsAndContext(t *testing.T) {
	p := corelaunch.Process{Prefix: []string{"codex"}}
	r := route.Route{Target: config.Target{Agent: "codex", Model: "gpt-6.1-sol", Effort: "high"}, Context: "minimal"}
	args := Pane{}.Argv(r, mode.Ask, p, "")
	for _, want := range []string{"tui.notifications=false", "notify=[]", `model_reasoning_effort="high"`, "project_doc_max_bytes=0"} {
		if !slices.Contains(args, want) {
			t.Errorf("%q missing from %v", want, args)
		}
	}
	if (Pane{}).Settings(agent.PaneStart{}) != nil {
		t.Fatal("Codex adds no pane settings")
	}
}
