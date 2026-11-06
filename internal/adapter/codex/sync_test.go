package codex_test

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"github.com/lorem-dev/duolog/internal/app/sync/synctest"
	"github.com/lorem-dev/duolog/internal/core/agentfile"
)

func TestFormatContract(t *testing.T) {
	synctest.FormatContract(t, func(*testing.T) agentfile.Format { return codex.Format{} }, synctest.CodexFixture())
}
