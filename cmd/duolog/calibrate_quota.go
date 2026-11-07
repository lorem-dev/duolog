package main

import (
	"context"
	"fmt"
	"io"

	"github.com/lorem-dev/duolog/internal/app/calibrate"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	appquota "github.com/lorem-dev/duolog/internal/app/quota"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/wiring"
)

// newCalibrationAccounts is calibration's view of the user's accounts in
// root: one account gate, opened on first use with cfg, whose route-aware
// cache holds every quota read of the command. d.Quota.Calibration replaces
// the reads in tests.
func newCalibrationAccounts(d *cmdDeps, cfg *config.Config, root string) *appquota.Accounts {
	return appquota.NewAccounts(d.Quota, func() (*paidruns.Gate, error) { return d.Delegate.PaidGate(cfg) }, cfg, root)
}

// planQuotas reads the quota of each registered agent for calibrate plan
// from source. A failed read is noted on out and leaves that agent unknown. A
// switched-off agent runs no cell, so its quota is not read.
func planQuotas(ctx context.Context, out io.Writer, cfg *config.Config, source *calibration.QuotaCache) map[string][]calibration.Window {
	quotas := map[string][]calibration.Window{}
	for _, agent := range wiring.Names() {
		if cfg.AgentSwitchedOff(agent) {
			continue
		}
		ws, err := source.Get(ctx, agent, false)
		if err != nil {
			fmt.Fprintf(out, "note: quota of %s unavailable: %v\n", agent, err)
			continue
		}
		quotas[agent] = ws
	}
	return quotas
}
