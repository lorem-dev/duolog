package delegate

import (
	"context"
	"errors"

	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/account"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
)

// No test runs a real agent to read its pace: an unreadable quota is unknown
// and balances nothing.
func init() {
	paidruns.PaceRead = func(context.Context, corelaunch.Process) (account.Snapshot, error) {
		return account.Snapshot{}, errors.New("no quota reads in tests")
	}
}
