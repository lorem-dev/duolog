package claude

import (
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/peer/peertest"
	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/screen"
)

const lspOffer = `Plugin: gopls-lsp
Marketplace: claude-plugins-official
Triggered by: .go files

Would you like to install this LSP plugin?
❯ 1. Yes, install gopls-lsp
  2. No, not now
`

func TestPaneAgentContract(t *testing.T) {
	peertest.PaneAgentContract(t, func(*testing.T) peertest.PaneAgent { return Pane{} })
}

func TestPaneOfferContract(t *testing.T) {
	s := screen.Parse([]byte(lspOffer), Facts.Pane)
	peertest.PaneOfferContract(t, func(*testing.T) peertest.PaneAgent { return Pane{} }, s, true, "gopls-lsp")
}

func TestPaneOffer(t *testing.T) {
	offer, ok := Pane{}.Offer(screen.Parse([]byte(lspOffer), Facts.Pane))
	if !ok || offer.Marketplace != "claude-plugins-official" || offer.Trigger != ".go files" || offer.By != "Claude" ||
		offer.Install != "claude plugin install gopls-lsp@claude-plugins-official" {
		t.Fatalf("%+v %v", offer, ok)
	}
}

func TestPaneArgvDisallowsSubagents(t *testing.T) {
	p := corelaunch.Process{Prefix: []string{"claude"}}
	r := route.Route{Target: config.Target{Agent: "claude", Model: "opus"}}
	args := Pane{}.Argv(r, mode.Ask, p)
	if !slices.Contains(args, "--disallowedTools=Agent,Task") {
		t.Fatalf("%v", args)
	}
	held := Pane{}.Argv(r, mode.Plan, p)
	if i := slices.IndexFunc(held, func(a string) bool { return strings.HasPrefix(a, "--disallowedTools=") }); i < 0 || !strings.Contains(held[i], "NotebookEdit") {
		t.Fatalf("a pane the gate holds read-only keeps its write tools: %v", held)
	}
}

func TestPaneSettingsTurnNotificationsOff(t *testing.T) {
	if got := (Pane{}).Settings(agent.PaneStart{})["preferredNotifChannel"]; got != "notifications_disabled" {
		t.Fatalf("%v", got)
	}
}
