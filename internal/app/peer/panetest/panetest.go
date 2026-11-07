// Package panetest gives the peer tests the real pane support of both agents,
// for the tests that assert what a real pane's argv and texts do. Only tests
// import it. The fake PaneAgent is in peertest.
package panetest

import (
	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"github.com/lorem-dev/duolog/internal/app/peer/peertest"
)

// Panes is the pane support of each registered agent, by name.
func Panes() map[string]peertest.PaneSupport {
	return map[string]peertest.PaneSupport{
		claude.Facts.Name: {Agent: claude.Pane{}, Facts: claude.Facts.Pane},
		codex.Facts.Name:  {Agent: codex.Pane{}, Facts: codex.Facts.Pane},
	}
}
