package main

import (
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	appquota "github.com/lorem-dev/duolog/internal/app/quota"
	"github.com/lorem-dev/duolog/internal/core/config"
)

// newCalibrationAccounts is calibration's view of the user's accounts in
// root: one account gate, opened on first use with cfg, whose route-aware
// cache holds every quota read of the command. d.Quota.Calibration replaces
// the reads in tests.
func newCalibrationAccounts(d *cmdDeps, cfg *config.Config, root string) *appquota.Accounts {
	return appquota.NewAccounts(d.Quota, func() (*paidruns.Gate, error) { return d.Delegate.PaidGate(cfg) }, cfg, root)
}
