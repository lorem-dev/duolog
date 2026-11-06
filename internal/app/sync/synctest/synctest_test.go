package synctest_test

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/app/sync/synctest"
	"github.com/lorem-dev/duolog/internal/core/agentfile"
)

func TestFormatContract(t *testing.T) {
	fx := synctest.ClaudeFixture()
	synctest.FormatContract(t, func(*testing.T) agentfile.Format { return synctest.NewFake(fx) }, fx)
}
