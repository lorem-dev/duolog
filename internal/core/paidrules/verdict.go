// Package paidrules decides how a launch is billed and what share of the
// account's windows duolog may use. It reads nothing: internal/app/paidruns
// reads the quota and the account state and asks it.
package paidrules

import (
	"slices"
	"strings"
	"time"

	// The file's account func decides the account kind.
	coreaccount "github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/launch"
)

// Verdict is how a launch shape of an agent is billed.
type Verdict string

const (
	// Subscription runs on a subscription window with paid overage off.
	Subscription Verdict = "subscription"
	// SubscriptionOverage runs on a window, but a run past it costs money.
	SubscriptionOverage Verdict = "subscription_overage"
	// Paid is billed per token, or unknown.
	Paid Verdict = "paid"
)

// Assessment is the verdict of one agent and launch shape, and the account
// kind with the share of each window duolog may use. Signals name the
// evidence that decided the verdict: the paid signals of a paid verdict, the
// overage signals of subscription_overage, none for subscription.
// AccountReasons name the workspace evidence. It holds names only.
//
// An assessment of the lead is reporting data: duolog never launches,
// changes or refuses the lead because of it.
type Assessment struct {
	Agent          string          `json:"agent"`
	Shape          launch.Shape    `json:"shape"`
	Variant        launch.Variant  `json:"variant"`
	Verdict        Verdict         `json:"verdict"`
	Account        string          `json:"account"`
	UsableShare    float64         `json:"usable_share"`
	Signals        []launch.Signal `json:"signals"`
	AccountReasons []string        `json:"account_reasons"`
}

// defaultUsableShare applies when the config has no valid share. The oldest
// quota read a verdict trusts is balance.max_age_seconds, quotaMaxAge.
const defaultUsableShare = 0.5

// Evaluate computes the verdict of process p from its route signals (R), the
// free read s and its error (W), and the run evidence e (E), at now:
//
//   - Any paid signal makes the verdict paid: a route signal of the process
//     or of the read, a failed, stale or missing read, no window with a
//     future reset, Claude windows not available, Codex ordinary usage not
//     allowed or unknown, a fast run, or a run that sent no expected
//     evidence. An unknown value counts as paid.
//   - A window never clears a route signal, and plan names decide nothing.
//   - Otherwise overage on, in the read or the run, or a run that used
//     overage, gives subscription_overage; the caller keeps passing that
//     run's evidence until a new successful read.
//   - Otherwise subscription.
//
// The account is workspace on any safe workspace evidence of the read or the
// run, or with paid_runs.<agent>.account: workspace; else personal. An
// explicit personal never hides workspace evidence.
func Evaluate(p launch.Process, s coreaccount.Snapshot, readErr error,
	e coreaccount.RunEvidence, cfg *config.Config, now time.Time) Assessment {
	a := Assessment{Agent: p.Agent, Shape: p.Shape, Variant: p.Variant}
	var paid, overage []launch.Signal
	add := func(list *[]launch.Signal, name, layer string) {
		*list = append(*list, launch.Signal{Name: name, Layer: layer})
	}
	read := readLayer(p.Agent)

	if p.Agent != "claude" && p.Agent != "codex" {
		add(&paid, "agent:unknown", "duolog")
	}
	// R: the process's environment, settings layers and argv.
	paid = append(paid, p.Signals...)
	// W: route evidence of the read is kept even when the read failed.
	b := s.Billing
	paid = append(paid, b.RouteSignals...)
	if readErr != nil {
		add(&paid, "read:failed", read)
	} else {
		if !firstParty(p.Agent, b) && len(b.RouteSignals) == 0 {
			add(&paid, "route:unknown", read)
		}
		if s.ReadAt.IsZero() || now.Sub(s.ReadAt) > quotaMaxAge(cfg) {
			add(&paid, "read:stale", read)
		}
		if !slices.ContainsFunc(s.Windows, func(w coreaccount.Window) bool { return w.ResetsAt.After(now) }) {
			add(&paid, "read:no-window", read)
		}
	}
	// A reply that refuses windows or ordinary usage is paid even from a
	// failed read; a missing permission only counts in a successful one.
	if p.Agent == "claude" && b.WindowsAvailable != nil && !*b.WindowsAvailable {
		add(&paid, "read:windows-unavailable", read)
	}
	if o := b.OrdinaryUsageAllowed; p.Agent == "codex" && (o != nil && !*o || o == nil && readErr == nil) {
		add(&paid, "read:ordinary-usage", read)
	}
	// E: what the last run showed holds for the next admission.
	if e.Fast {
		add(&paid, "run:fastMode", "run")
	}
	if e.Expected && !e.Seen {
		add(&paid, "run:no-evidence", "run")
	}
	if e.UsingOverage {
		add(&overage, "run:overage", "run")
	}
	if b.OverageEnabled {
		add(&overage, "overage:enabled", read)
	}
	if e.OverageEnabled {
		add(&overage, "overage:enabled", "run")
	}

	switch {
	case len(paid) > 0:
		a.Verdict, a.Signals = Paid, sortedSignals(paid)
	case len(overage) > 0:
		a.Verdict, a.Signals = SubscriptionOverage, sortedSignals(overage)
	default:
		a.Verdict = Subscription
	}
	a.Account, a.UsableShare, a.AccountReasons = account(p.Agent, b, e, cfg)
	return a
}

// firstParty reports whether the read named a first-party subscription
// login: claude.ai or an OAuth token on the first-party API for Claude, a
// chatgpt account, auth mode and stored login for Codex.
func firstParty(agent string, b coreaccount.BillingFacts) bool {
	if !b.RouteKnown {
		return false
	}
	switch agent {
	case "claude":
		return (b.AuthMethod == "claude.ai" || b.AuthMethod == "oauth_token") &&
			(b.APIProvider == "" || b.APIProvider == "firstParty")
	case "codex":
		return b.AccountType == "chatgpt" && (b.AuthMode == "" || b.AuthMode == "chatgpt") &&
			(b.AuthMethod == "" || b.AuthMethod == "chatgpt")
	}
	return false
}

// readLayer names the free read that window signals come from.
func readLayer(agent string) string {
	switch agent {
	case "claude":
		return "/usage"
	case "codex":
		return "account/rateLimits/read"
	}
	return "quota read"
}

// account decides the account kind and its usable share. The snapshot's plan
// name is not evidence; only safe workspace categories, among them the
// read's own workspace_plan, and the config's workspace are.
func account(agent string, b coreaccount.BillingFacts, e coreaccount.RunEvidence, cfg *config.Config) (string, float64, []string) {
	var reasons []string
	for _, reason := range slices.Concat(b.WorkspaceReasons, e.WorkspaceReasons) {
		if coreaccount.SafeWorkspaceReason(reason) && !slices.Contains(reasons, reason) {
			reasons = append(reasons, reason)
		}
	}
	share := defaultUsableShare
	if cfg != nil {
		if cfg.PaidRuns.Agent(agent).Account == "workspace" {
			reasons = append(reasons, "paid_runs."+agent+".account")
		}
		if u := cfg.PaidRuns.UsableShare; u > 0 && u <= 1 {
			share = u
		}
	}
	if len(reasons) == 0 {
		return "personal", 1, nil
	}
	return "workspace", share, reasons
}

// FamilyBlocked reports whether s refuses runs of a model family: the whole
// account is rejected or at its spend control, or a window of the family's
// own limits is rejected. A scoped rejection blocks its family only. An
// empty family asks about the account alone.
func FamilyBlocked(s coreaccount.Snapshot, family string) bool {
	if s.Rejected || s.SpendControlReached {
		return true
	}
	if family == "" {
		return false
	}
	return slices.ContainsFunc(s.ModelLimits[family], coreaccount.Window.Rejected)
}

func sortedSignals(list []launch.Signal) []launch.Signal {
	list = slices.Clone(list)
	slices.SortFunc(list, func(a, b launch.Signal) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.Layer, b.Layer)
	})
	return slices.Compact(list)
}
