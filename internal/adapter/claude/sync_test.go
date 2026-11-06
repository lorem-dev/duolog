package claude_test

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/app/sync/synctest"
	"github.com/lorem-dev/duolog/internal/core/agentfile"
)

func TestFormatContract(t *testing.T) {
	synctest.FormatContract(t, func(*testing.T) agentfile.Format { return claude.Format{} }, synctest.ClaudeFixture())
}
