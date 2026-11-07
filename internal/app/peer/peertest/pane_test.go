package peertest

import "testing"

func TestPaneAgentContract(t *testing.T) {
	PaneAgentContract(t, func(*testing.T) PaneAgent { return &Fake{} })
}

func TestPaneContract(t *testing.T) {
	PaneContract(t, func(*testing.T) Pane { return &FakePane{LeadSession: "sess", Script: ContractScript()} })
}
