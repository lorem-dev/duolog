package calibration

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
)

// Verdicts of an agent's route, as the account gate gives them.
const (
	VerdictSubscription = "subscription"
	VerdictOverage      = "subscription_overage"
	VerdictPaid         = "paid"
)

// BillingBudget is how an agent's calibration steps are billed, as the
// account gate reports its worker route when the plan is made: the verdict,
// the account kind and the share of each window duolog may use, the agent's
// paid spend of the last 24 hours, its paid tasks in flight and its daily
// cap, and the paid_runs opt-ins. ApprovedUSD is what the plan approves to
// spend on a paid route: its cells, judge calls and rewrites at their high
// estimates. USD is real money, never scaled by the usable share.
type BillingBudget struct {
	Verdict     string  `json:"verdict"`
	Account     string  `json:"account"`
	UsableShare float64 `json:"usable_share"`
	SpentUSD    float64 `json:"spent_usd"`
	InflightUSD float64 `json:"inflight_usd"`
	CapUSD      float64 `json:"cap_usd"`
	ApprovedUSD float64 `json:"approved_usd"`
	Allow       bool    `json:"allow"`
	Calibrate   bool    `json:"calibrate"`
}

// usable is the share of each window the budget's account may use: 1 for a
// personal account or an unknown share.
func (b BillingBudget) usable() float64 {
	if b.UsableShare > 0 && b.UsableShare <= 1 {
		return b.UsableShare
	}
	return 1
}

// paid reports a route billed per token.
func (b BillingBudget) paid() bool { return b.Verdict == VerdictPaid }

// opted reports both opt-ins a paid calibration step needs.
func (b BillingBudget) opted() bool { return b.Allow && b.Calibrate }

// remaining is the USD a paid route may still spend: the cap less the last
// 24 hours' spend and the tasks in flight.
func (b BillingBudget) remaining() float64 {
	return max(0, b.CapUSD-b.SpentUSD-b.InflightUSD)
}

// usdSlack absorbs float rounding at an exact cap.
const usdSlack = 1e-9

// LimitPaidPlan checks p against each agent's billing and records it. A paid
// route that a step of p uses (a cell, a judge call or a rewrite) needs
// paid_runs.<agent>.allow and calibrate, and the high USD of all its steps,
// judge calls and rewrites included, must fit what the agent's cap leaves;
// BuildPlan cuts the plan until it does. Each agent's budget, with the
// approved USD of a paid route, goes into p.Billing and so into the hash: a
// changed route, account, share or approved USD needs a new approval.
// Agents without a budget keep the plan as it was.
func LimitPaidPlan(p Plan, budgets map[string]BillingBudget) (Plan, error) {
	var billing map[string]BillingBudget
	for _, agent := range slices.Sorted(maps.Keys(budgets)) {
		if !p.HasAgent(agent) && !p.HasJudge(agent) {
			continue
		}
		b := budgets[agent]
		b.ApprovedUSD = 0
		if b.paid() {
			if !b.opted() {
				return Plan{}, fmt.Errorf("calibration plan: %s runs on a paid route; calibrating it needs paid_runs.%s.allow with a daily_cap_usd and paid_runs.%s.calibrate", agent, agent, agent)
			}
			usd := planUSD(p, agent)
			if usd > b.remaining()+usdSlack {
				return Plan{}, fmt.Errorf("calibration plan: %s needs %.2f USD, but its cap of %s USD leaves %.2f after %.2f spent and %.2f in flight",
					agent, usd, number(b.CapUSD), b.remaining(), b.SpentUSD, b.InflightUSD)
			}
			b.ApprovedUSD = math.Round(usd*1e6) / 1e6
		}
		b.UsableShare = b.usable()
		if billing == nil {
			billing = map[string]BillingBudget{}
		}
		billing[agent] = b
	}
	p.Billing = billing
	p.Hash = p.ComputeHash()
	return p, nil
}

// planUSD is the high USD of agent's steps in p: cells, judge calls and
// rewrites.
func planUSD(p Plan, agent string) float64 {
	var usd float64
	for _, c := range p.Classes {
		usd += c.AgentUSD[agent] + c.JudgeUSD[agent]
	}
	return usd
}

// LimitBilling is the budget each agent of the approved plan p runs with
// now: the stricter of the approved one and current, the gate's report of
// today. Both opt-ins must hold in both, the lower cap and the smaller usable
// share win, a workspace account stays one, and the spend is today's. A
// route that turned paid after approval is paid work the plan never
// approved, so it is not allowed. An agent current does not report keeps its
// approved budget.
func (p Plan) LimitBilling(current map[string]BillingBudget) map[string]BillingBudget {
	out := map[string]BillingBudget{}
	for agent, approved := range p.Billing {
		b := approved
		if cur, ok := current[agent]; ok {
			b.Allow = approved.Allow && cur.Allow
			b.Calibrate = approved.Calibrate && cur.Calibrate
			b.UsableShare = min(approved.usable(), cur.usable())
			if cur.Account == "workspace" {
				b.Account = "workspace"
			}
			if cur.paid() && !approved.paid() {
				b.Verdict, b.Allow = VerdictPaid, false
			} else if cur.Verdict == VerdictOverage && approved.Verdict == VerdictSubscription {
				b.Verdict = VerdictOverage
			}
			b.CapUSD = min(approved.CapUSD, cur.CapUSD)
			b.SpentUSD, b.InflightUSD = cur.SpentUSD, cur.InflightUSD
		}
		out[agent] = b
	}
	// A plan without a budget for an agent, such as one approved before paid
	// budgets, never approved paid work for it.
	for agent, cur := range current {
		if _, ok := out[agent]; !ok && cur.paid() {
			cur.Allow = false
			out[agent] = cur
		}
	}
	return out
}

// AdmitBilled decides a calibration step of agent billed as b. A paid route
// needs no window: it is stopped without its opt-ins or once the run spent
// the plan's approved USD, and paused while the agent's 24-hour cap has
// nothing left, for a resume to read the budget again; each worker's money
// is then admitted by the account gate when it starts. Any other route keeps
// Admit's window rules on the usable share of its account, which a personal
// account leaves as they were.
func AdmitBilled(agent string, b BillingBudget, ws []Window, cost CellCost, spend Spend, cfg config.Calibration, held float64, now time.Time) Decision {
	if b.paid() {
		switch {
		case !b.opted():
			return Decision{Stop: "paid calibration not allowed"}
		case b.ApprovedUSD > 0 && spend.PaidUSD[agent] >= b.ApprovedUSD-usdSlack:
			return Decision{Stop: stopCap}
		case b.CapUSD > 0 && b.remaining() <= usdSlack:
			return Decision{Wait: now.Add(capRetry)}
		}
		return Decision{Admit: true}
	}
	return admitShare(agent, ws, cost, spend, cfg, held, now, b.usable())
}

// billingLines say how each agent of p is billed: a paid route's high USD
// estimate, its spend, its cap and the price caveat; a workspace account's
// usable share with its caps and reserves; paid overage that the plan keeps
// below the lead reserve.
func billingLines(p Plan) []string {
	var lines []string
	for _, agent := range slices.Sorted(maps.Keys(p.Billing)) {
		b := p.Billing[agent]
		if b.paid() {
			lines = append(lines, fmt.Sprintf("paid %s: %.2f USD high estimate for its cells, judge calls and rewrites; spent %.2f and %.2f in flight of the %s USD cap in the last 24 hours; prices are a lower bound: fast mode and cloud surcharges are not priced",
				agent, b.ApprovedUSD, b.SpentUSD, b.InflightUSD, number(b.CapUSD)))
		}
		if u := b.usable(); u < 1 {
			lines = append(lines, fmt.Sprintf("account %s: workspace, usable share %s: caps %s%% weekly and %s%% of a five-hour window of the whole window; lead reserves %s%% weekly and %s%% five-hour of the usable part",
				agent, number(u), number(math.Round(p.CapWeekly*u*1e6)/1e6), number(math.Round(p.CapWindow*u*1e6)/1e6), number(ReserveWeekly), number(ReserveWindow)))
		}
		if b.Verdict == VerdictOverage {
			lines = append(lines, fmt.Sprintf("%s: paid overage is on; the plan fits below the lead reserve and duolog stops before overage", agent))
		}
	}
	return lines
}

// overageRoom is the weekly points a plan of an agent on paid overage may
// take: what the account's weekly window leaves below the lead reserve of
// its usable part, after the reading. Without a reading it leaves the cap.
func overageRoom(quota map[string][]Window, agent string, u float64) (float64, bool) {
	w := accountWindow(quota[agent], KindWeekly)
	if w == nil {
		return 0, false
	}
	return (100-ReserveWeekly)*u - w.UsedPercent, true
}
