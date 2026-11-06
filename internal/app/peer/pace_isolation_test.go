package peer

import (
	"context"
	"errors"

	"github.com/lorem-dev/duolog/internal/app/paidruns"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/quota"
)

// No test runs a real agent to read its pace: an unreadable quota is unknown
// and balances nothing.
func init() {
	paidruns.PaceRead = func(context.Context, corelaunch.Process) (quota.Snapshot, error) {
		return quota.Snapshot{}, errors.New("no quota reads in tests")
	}
}
