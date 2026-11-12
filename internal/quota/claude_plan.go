package quota

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
)

// PlanRead is the plan of a Claude login as its auth status names it. Plan is
// duolog's name for it ("" when auth status names none duolog knows), and
// Subscription and Tier are the fields it came from.
type PlanRead struct {
	Plan, Subscription, Tier string
}

// Source says where the plan came from, for install and doctor: the command
// and the fields it printed.
func (p PlanRead) Source() string {
	s := "claude auth status: subscriptionType " + shownField(p.Subscription)
	if p.Tier != "" {
		s += ", planTier " + shownField(p.Tier)
	}
	return s
}

// shownField keeps a field of auth status that is safe to print: a short
// word of lower-case letters, digits and underscores, or "none".
func shownField(v string) string {
	if v == "" || len(v) > 40 {
		return "none"
	}
	for _, c := range v {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return "unreadable"
		}
	}
	return v
}

// ReadClaudePlan reads the Claude plan that a free worker of duolog's own
// environment sees, from auth status alone: no /usage, no model call, so it
// is quick and does not compete with the quota reads of running workers.
// Timeout bounds the whole read.
func ReadClaudePlan(ctx context.Context, timeout time.Duration) (PlanRead, error) {
	p, err := freeProcess("claude")
	if err != nil {
		return PlanRead{}, err
	}
	if len(p.Prefix) == 0 {
		return PlanRead{}, errors.New("not installed")
	}
	bin, err := lookPath(p.Prefix[0], p.Env)
	if err != nil {
		return PlanRead{}, errors.New("not installed")
	}
	dir, err := os.MkdirTemp("", "duolog-plan-")
	if err != nil {
		return PlanRead{}, errors.New("cannot create temporary folder")
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := runner{p: p, bin: bin, dir: dir}.command(ctx, "auth", "status", "--json")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return PlanRead{}, errors.New("cannot open auth status output")
	}
	if err := cmd.Start(); err != nil {
		return PlanRead{}, errors.New("cannot start auth status")
	}
	data, readErr := io.ReadAll(io.LimitReader(out, 64*1024))
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return PlanRead{}, ErrTimedOut
	}
	if readErr != nil || waitErr != nil {
		return PlanRead{}, errors.New("auth status failed")
	}
	return parseClaudePlan(data)
}

// parseClaudePlan reads the plan fields of an auth status reply.
func parseClaudePlan(data []byte) (PlanRead, error) {
	var status struct {
		LoggedIn         *bool  `json:"loggedIn"`
		SubscriptionType string `json:"subscriptionType"`
		PlanTier         string `json:"planTier"`
	}
	if json.Unmarshal(data, &status) != nil {
		return PlanRead{}, errors.New("auth status is not JSON")
	}
	if status.LoggedIn != nil && !*status.LoggedIn {
		return PlanRead{}, errors.New("not logged in")
	}
	return PlanRead{
		Plan:         claudePlan(status.SubscriptionType, status.PlanTier),
		Subscription: status.SubscriptionType,
		Tier:         status.PlanTier,
	}, nil
}
