package paidrules

import (
	"testing"

	coreaccount "github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/launch"
)

// Kills requiring a session window for a subscription, a measured tier or weekly pace.
func TestWeeklyOnlySubscription(t *testing.T) {
	s := snapshot("claude")
	s.Windows = s.Windows[1:]
	p := process("claude", launch.Worker, launch.Free)
	cfg := tierConfig()
	a := Evaluate(p, s, nil, coreaccount.RunEvidence{}, cfg, now)
	if a.Verdict != Subscription {
		t.Fatalf("assessment %+v", a)
	}
	tier := SelectTier(a, s, []RateSample{rate("claude", "opus", "weekly_all", .001, 3)}, TierResult{}, cfg)
	if tier.Tier != TierLarge {
		t.Fatalf("tier %+v", tier)
	}
	pace := AgentPace(p, s, nil, cfg, now)
	if !pace.Subscription || pace.Week == nil || pace.FiveHour != nil {
		t.Fatalf("pace %+v", pace)
	}
}
