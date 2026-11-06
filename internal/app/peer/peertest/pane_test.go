package peertest

import "testing"

func TestPaneAgentContract(t *testing.T) {
	PaneAgentContract(t, func(*testing.T) PaneAgent { return &Fake{} })
}
