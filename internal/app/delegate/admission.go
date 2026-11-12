package delegate

import (
	"context"

	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/config"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
)

// admissionState opens the account gate and reads pace only for a worker
// candidate. A known lead placement needs neither, even if quota is slow.
type admissionState struct {
	accounts *paidruns.Gate
	balance  *coreroute.Balance
	balanced bool
}

func (a *admissionState) gate(d Deps, cfg *config.Config) (*paidruns.Gate, error) {
	if a.accounts == nil {
		var err error
		if a.accounts, err = d.PaidGate(cfg); err != nil {
			return nil, err
		}
	}
	return a.accounts, nil
}

func (a *admissionState) resolve(ctx context.Context, cfg *config.Config, role string, unavailable map[string]bool,
	ro coreroute.Options, opts Options, free bool) (coreroute.Route, error) {
	route, err := coreroute.Resolve(cfg, role, unavailable, ro)
	if err != nil || route.Self || route.InLead || !free || opts.Entry != nil {
		return route, err
	}
	if !a.balanced {
		a.balanced = true
		if cfg.Routing.Balance == "pace" {
			clear := opts.admissionWait("waiting for quota to balance routes")
			a.balance = paidruns.DefaultPaceBalance(ctx, cfg)
			clear()
		}
	}
	ro.Balance = a.balance
	return coreroute.Resolve(cfg, role, unavailable, ro)
}

// admissionWait reports one synchronous wait, returning its clear function.
func (o Options) admissionWait(reason string) func() {
	if o.AdmissionWait == nil {
		return func() {}
	}
	o.AdmissionWait(reason)
	return func() { o.AdmissionWait("") }
}
