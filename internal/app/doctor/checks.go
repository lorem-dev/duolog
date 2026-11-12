package doctor

import (
	"context"
	"errors"
	"time"

	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/launch"
)

// Each sequential check starts its own budget, still bounded by the caller.
// Tests shorten it to exercise an exhausted check without a minute's wait.
var checkTimeout = time.Minute

// billingGate opens the account gate of cfg and checks that its state can be
// read now (billingState).
func billingGate(ctx context.Context, d Deps, cfg *config.Config) (*paidruns.Gate, error) {
	g, err := d.PaidGate(cfg)
	if err != nil {
		return nil, err
	}
	return g, billingState(ctx, g)
}

// billingState avoids starting quota subprocesses whose identity recording
// would wait behind an already busy account state. No lock spans a quota read.
func billingState(ctx context.Context, g *paidruns.Gate) error {
	if g.Store == nil {
		return errors.New("paid runs: the gate needs a store")
	}
	ctx, cancel := context.WithTimeout(ctx, min(time.Second, checkTimeout))
	defer cancel()
	return g.Store.View(ctx, func(paidruns.State) error { return nil })
}

func billingReport(ctx context.Context, g *paidruns.Gate, in launch.ProcessInput) (paidruns.Report, error) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	return g.Report(ctx, in)
}

func reportUnavailable(r paidruns.Report, err error) bool {
	return r.Assessment.Agent == "" || errors.Is(err, account.ErrStateBusy)
}

func checkError(err error) string {
	if errors.Is(err, account.ErrStateBusy) {
		return account.ErrStateBusy.Error() + "; check unavailable, not an account fault; retry when it is idle"
	}
	return err.Error()
}
