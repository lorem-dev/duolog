package wiring

import (
	"context"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/app/install"
	"github.com/lorem-dev/duolog/internal/quota"
)

// leadReadTimeout bounds the quota read that names the lead's plan at
// install. The read's own budget is shorter; this one stops a hang.
const leadReadTimeout = 45 * time.Second

// leadPlan reads the Claude plan that a free worker of duolog's own
// environment sees, as the quota read that admits a worker names it: the
// lead runs on the same login. It is "" when the read fails or names none.
// Tests replace it.
var leadPlan = func() string {
	ctx, cancel := context.WithTimeout(context.Background(), leadReadTimeout)
	defer cancel()
	s, err := quota.Reader{Once: true}.Read(ctx, "claude")
	if err != nil {
		return ""
	}
	return s.Plan
}

// leadWindow is install's LeadWindow: the workers.compact_window rule of the
// config at root, for the plan leadPlan reads. Only auto reads a plan; a plan
// that is not read gets the bounded fallback, the smallest ladder value.
func leadWindow(root string) (install.LeadWindow, error) {
	cfg, err := configfile.Load(root)
	if err != nil {
		return install.LeadWindow{}, err
	}
	plan := ""
	if cfg.Workers.CompactWindow == "auto" {
		plan = leadPlan()
	}
	return install.LeadWindow{Window: cfg.Workers.Window(plan), Plan: plan}, nil
}
