package wiring

import (
	"context"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/app/install"
	"github.com/lorem-dev/duolog/internal/quota"
)

// leadReadTimeout bounds the auth status read that names the lead's plan at
// install. A cold start of the CLI on a busy machine can take seconds.
const leadReadTimeout = 20 * time.Second

// leadPlan reads the Claude plan that a free worker of duolog's own
// environment sees, from Claude's auth status alone: the lead runs on the
// same login. Source says where the plan came from, or why it was not read.
// Tests replace it.
var leadPlan = func() (plan, source string, ok bool) {
	p, err := quota.ReadClaudePlan(context.Background(), leadReadTimeout)
	if err != nil {
		return "", "claude auth status: " + err.Error(), false
	}
	return p.Plan, p.Source(), true
}

// leadWindow is install's LeadWindow: the workers.compact_window rule of the
// config at root, for the plan leadPlan reads. Only auto reads a plan; a plan
// that is not read gets the bounded fallback, the smallest ladder value, and
// install keeps a window it wrote before instead.
func leadWindow(root string) (install.LeadWindow, error) {
	cfg, err := configfile.Load(root)
	if err != nil {
		return install.LeadWindow{}, err
	}
	if cfg.Workers.CompactWindow != "auto" {
		return install.LeadWindow{Window: cfg.Workers.Window("")}, nil
	}
	plan, source, ok := leadPlan()
	return install.LeadWindow{Window: cfg.Workers.Window(plan), Plan: plan, Source: source, Fallback: !ok || plan == ""}, nil
}
