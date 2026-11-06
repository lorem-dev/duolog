package codex_test

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"github.com/lorem-dev/duolog/internal/app/delegate/delegatetest"
)

func TestTrustContract(t *testing.T) {
	delegatetest.TrustContract(t, func(*testing.T) delegatetest.TrustPort { return codex.Trust{} })
}
