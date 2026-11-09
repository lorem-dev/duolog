package route

import (
	"fmt"
	"math"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
)

// fiveHourLimit is the used share of the five-hour window above which an
// agent counts as ahead of pace whatever its week shows.
const fiveHourLimit = .80

// PaceWindow is one quota window as shares of the window, each from 0 to 1.
type PaceWindow struct {
	Used    float64
	Elapsed float64
}

// AgentPace is what pace balancing knows of one agent. Subscription is true
// only for a fresh read with the subscription verdict: paid, overage and
// unknown routes are never balanced into. A missing Week means the quota is
// unknown.
type AgentPace struct {
	Subscription bool
	Week         *PaceWindow
	FiveHour     *PaceWindow
}

// Elapsed is the share of a window of length d that has passed when it
// resets at resetsAt. A reset at or before now means the read is stale, a
// reset time that is missing or a window without a length cannot be placed,
// and none of them has a share. A reset more than d away, from clock skew,
// counts as a window that has just reset.
func Elapsed(resetsAt time.Time, d time.Duration, now time.Time) (float64, bool) {
	left := resetsAt.Sub(now)
	if resetsAt.IsZero() || d <= 0 || left <= 0 {
		return 0, false
	}
	return math.Min(math.Max(1-left.Seconds()/d.Seconds(), 0), 1), true
}

// Ahead reports whether the agent runs ahead of pace: its weekly used share
// exceeds the elapsed share by more than margin, or its five-hour window is
// above 0.80 used. Without a weekly window the quota is unknown and the
// agent is never ahead. An agent that was ahead (was) stays ahead until its
// week falls below pace minus margin, so a use agent does not flap at the
// edge.
func Ahead(a AgentPace, margin float64, was bool) bool {
	if a.Week == nil {
		return false
	}
	if a.FiveHour != nil && a.FiveHour.Used > fiveHourLimit {
		return true
	}
	if was {
		return a.Week.Used >= a.Week.Elapsed-margin
	}
	return a.Week.Used > a.Week.Elapsed+margin
}

// Why states what puts the agent ahead of pace, for the route reason.
func (a AgentPace) Why(agent string) string {
	if a.FiveHour != nil && a.FiveHour.Used > fiveHourLimit {
		return fmt.Sprintf("%s %s of the five hours", agent, percent(a.FiveHour.Used))
	}
	if a.Week == nil {
		return agent + " quota unknown"
	}
	return fmt.Sprintf("%s %s of the week at %s elapsed", agent, percent(a.Week.Used), percent(a.Week.Elapsed))
}

func percent(share float64) string { return fmt.Sprintf("%.0f%%", share*100) }

// Balance decides, from the agents' paces, whether a new task starts on a
// role's fallback. An agent absent from Pace has unknown quota. Was holds the
// agents that an earlier decision found ahead, for hysteresis.
type Balance struct {
	// Mode is the routing.balance setting, pace or off.
	Mode   string
	Margin float64
	Pace   map[string]AgentPace
	Was    map[string]bool
}

// Decision is the outcome of Balance.Decide.
type Decision struct {
	// Move is true when the task starts on the role's fallback.
	Move bool
	// Reason names the role, the fallback and the use agent's pace; empty
	// unless Move.
	Reason string
	// Ahead is the use agent's state after this decision, for the next one.
	Ahead map[string]bool
}

// Decide applies the pace rules to role. The use agent moves to the
// fallback only when it is ahead of pace and the fallback is another agent
// that is not ahead of pace and runs on a subscription. Unknown or stale
// quota, a paid or overage route, a fallback that is ahead too, and a role
// without a named fallback leave use in place, and so does a role with
// balance set to false.
func (b Balance) Decide(role string, spec config.Role) Decision {
	d := Decision{Ahead: map[string]bool{}}
	use, fb := spec.Use, spec.Fallback
	if b.Mode != "pace" || use.Agent == "" || !spec.Balanced() {
		return d
	}
	u, ok := b.Pace[use.Agent]
	if !ok || !u.Subscription || u.Week == nil {
		return d
	}
	d.Ahead[use.Agent] = Ahead(u, b.Margin, b.Was[use.Agent])
	if !d.Ahead[use.Agent] || fb.Agent == "" || fb.Agent == use.Agent || fb.Model == "" {
		return d
	}
	f, ok := b.Pace[fb.Agent]
	if !ok || !f.Subscription || f.Week == nil || Ahead(f, b.Margin, false) {
		return d
	}
	d.Move = true
	d.Reason = fmt.Sprintf("%s -> %s %s: %s", role, fb.Agent, fb.Model, u.Why(use.Agent))
	return d
}
