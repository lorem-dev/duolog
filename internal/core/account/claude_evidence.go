package account

import "encoding/json"

// ClaudeRateLimit reads the rate_limit_info of one rate_limit_event as run
// evidence. It keeps flags and the workspace level of an overage disabled
// reason, and explicit quota windows, never a raw reason or identity.
// Malformed billing fields count as absent.
func ClaudeRateLimit(info json.RawMessage) (RunEvidence, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(info, &fields) != nil || fields == nil {
		return RunEvidence{}, false
	}
	set := func(name string) bool { return string(fields[name]) == "true" }
	is := func(name string, values ...string) bool {
		var value string
		if json.Unmarshal(fields[name], &value) != nil {
			return false
		}
		for _, v := range values {
			if value == v {
				return true
			}
		}
		return false
	}
	e := RunEvidence{
		Expected: true,
		Seen:     true,
		Windows:  ClaudeWindows(info),
		// The run spends paid overage now.
		UsingOverage: set("isUsingOverage") || set("overageInUse") || is("rateLimitType", "overage"),
		// Paid overage is provisioned: a run past the window costs money.
		OverageEnabled: set("overageEnabled") || is("overageStatus", "allowed", "allowed_warning"),
	}
	var reason string
	if json.Unmarshal(fields["overageDisabledReason"], &reason) == nil {
		if category := claudeWorkspaceReason(reason); category != "" {
			e.WorkspaceReasons = []string{category}
		}
	}
	return e, true
}

// claudeWorkspaceReason is the workspace level of an overage disabled reason
// set at org, seat tier, group or member level, as the quota read keeps it
// (quota.BillingFacts.WorkspaceReasons), or "" for any other reason.
func claudeWorkspaceReason(reason string) string {
	switch reason {
	case "org_level_disabled", "org_level_disabled_until", "org_service_level_disabled":
		return "org"
	case "seat_tier_level_disabled", "seat_tier_zero_credit_limit":
		return "seat_tier"
	case "group_zero_credit_limit":
		return "group"
	case "member_level_disabled", "member_zero_credit_limit":
		return "member"
	}
	return ""
}
