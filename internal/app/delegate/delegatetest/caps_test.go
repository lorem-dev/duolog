package delegatetest

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/app/delegate"
)

func TestCapabilitiesContract(t *testing.T) {
	CapabilitiesContract(t, "codex", func(*testing.T) CapsPort { return &Caps{Agent: "codex"} })
	// The fake is a delegate.Capabilities.
	var _ delegate.Capabilities = &Caps{}
}
