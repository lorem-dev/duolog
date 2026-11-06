package paidrules

import (
	"time"

	coreaccount "github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/route"
)

// paceAgents are the agents whose pace is read.
var paceAgents = []string{"claude", "codex"}

// AgentPace turns one free read of process p into the input of pace
// balancing. An agent counts as a subscription only when its verdict is
// subscription, which needs a fresh read with a future window, no paid
// signal and no overage; any other read, a failed, stale or reset one
// included, leaves no weekly window, so its quota is unknown and no task is
// balanced to or from it. Used shares are scaled by 1/u on a workspace
// account and never pass 1. Only the unscoped weekly and five-hour windows
// count.
func AgentPace(p launch.Process, s coreaccount.Snapshot, readErr error,
	cfg *config.Config, now time.Time) route.AgentPace {
	a := Evaluate(p, s, readErr, coreaccount.RunEvidence{}, cfg, now)
	if a.Verdict != Subscription {
		return route.AgentPace{}
	}
	scale := 1.0
	if a.UsableShare > 0 && a.UsableShare <= 1 {
		scale = 1 / a.UsableShare
	}
	out := route.AgentPace{Subscription: true}
	for _, w := range s.Windows {
		if w.Model != "" || w.UsedPercent < 0 {
			continue
		}
		elapsed, ok := route.Elapsed(w.ResetsAt, time.Duration(w.DurationMinutes)*time.Minute, now)
		if !ok {
			continue
		}
		pw := &route.PaceWindow{Used: min(w.UsedPercent/100*scale, 1), Elapsed: elapsed}
		switch w.Kind {
		case "weekly_all":
			out.Week = pw
		case "session":
			out.FiveHour = pw
		}
	}
	if out.Week == nil {
		return route.AgentPace{}
	}
	return out
}
