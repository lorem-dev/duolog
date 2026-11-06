package delegatetest

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/app/delegate"
)

func TestTrustContract(t *testing.T) {
	TrustContract(t, func(*testing.T) TrustPort { return &Trust{} })
	// The fake is a delegate.Trust.
	var _ delegate.Trust = &Trust{}
}
