// Package account holds the billing facts of an account that a run reports
// about itself. It reads nothing: internal/quota reads the account, and the
// adapters fill these values from what an agent's CLI printed.
package account

import "slices"

// RunEvidence is what a run told about its own billing, E in the design:
// Claude's rate_limit_event on stream-json. The stream parser fills it with
// flags, safe categories and quota windows, never a raw reason or identity.
// Expected says the run should send evidence and Seen that it did;
// UsingOverage that the run spent paid overage, OverageEnabled that overage
// is on, Fast that the run used fast mode. WorkspaceReasons are the safe
// categories of BillingFacts.WorkspaceReasons. Windows holds the latest
// explicit unified windows; an event without windows leaves them unchanged.
type RunEvidence struct {
	Expected, Seen, UsingOverage, OverageEnabled, Fast bool
	WorkspaceReasons                                   []string
	Windows                                            []Window
}

// Add takes a later event of the same run. Every flag only turns on, so a
// benign event never clears what an earlier one showed. Reasons outside the
// safe categories are dropped.
func (e *RunEvidence) Add(later RunEvidence) {
	e.Expected = e.Expected || later.Expected
	e.Seen = e.Seen || later.Seen
	e.UsingOverage = e.UsingOverage || later.UsingOverage
	e.OverageEnabled = e.OverageEnabled || later.OverageEnabled
	e.Fast = e.Fast || later.Fast
	if len(later.Windows) > 0 {
		e.Windows = slices.Clone(later.Windows)
	}
	for _, reason := range later.WorkspaceReasons {
		if SafeWorkspaceReason(reason) && !slices.Contains(e.WorkspaceReasons, reason) {
			e.WorkspaceReasons = append(e.WorkspaceReasons, reason)
		}
	}
}

// Charged reports whether the run this evidence describes counts as paid: it
// used overage or fast mode, or it should have sent evidence and did not.
func (e RunEvidence) Charged() bool {
	return e.UsingOverage || e.Fast || (e.Expected && !e.Seen)
}

// workspaceReasons are the categories of workspace evidence the reads keep:
// the level of a Claude overage disabled reason, and the Codex workspace
// name, workspace plan type, forced workspace id, reached spend control and
// workspace reached types.
var workspaceReasons = []string{
	"org", "seat_tier", "group", "member",
	"workspace_name", "workspace_plan", "forced_workspace", "spend_control",
	"workspace_owner_credits_depleted", "workspace_member_credits_depleted",
	"workspace_owner_usage_limit_reached", "workspace_member_usage_limit_reached",
}

// SafeWorkspaceReason reports whether category is one of the workspace
// evidence categories, the only reasons duolog keeps or shows.
func SafeWorkspaceReason(category string) bool {
	return slices.Contains(workspaceReasons, category)
}
