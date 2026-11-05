package quota

import (
	"slices"

	"github.com/lorem-dev/duolog/internal/workerenv"
)

// BillingFacts is the billing evidence of a free read. It keeps known
// spellings, safe categories and flags only: a value outside the known
// spellings becomes "unknown", and identity, credentials and raw reasons are
// discarded. Absent evidence stays absent; nothing here is inferred.
type BillingFacts struct {
	AuthMethod  string `json:"auth_method,omitempty"`
	APIProvider string `json:"api_provider,omitempty"`
	AccountType string `json:"account_type,omitempty"`
	AuthMode    string `json:"auth_mode,omitempty"`
	// RouteKnown is true when the read named a known route, paid or not. A
	// failed or unknown reply leaves it false with a signal that says why.
	RouteKnown   bool               `json:"route_known"`
	RouteSignals []workerenv.Signal `json:"route_signals,omitempty"`
	// WindowsAvailable is the reply's own flag, nil when the reply has none.
	WindowsAvailable     *bool    `json:"windows_available,omitempty"`
	OrdinaryUsageAllowed *bool    `json:"ordinary_usage_allowed,omitempty"`
	SpendControlPresent  bool     `json:"spend_control_present,omitempty"`
	WorkspaceReasons     []string `json:"workspace_reasons,omitempty"`
	OverageEnabled       bool     `json:"overage_enabled,omitempty"`
	ProviderEnvKey       string   `json:"provider_env_key,omitempty"`
}

const claudeAuthLayer = "auth status"

func (b *BillingFacts) signal(name, layer string) {
	b.RouteSignals = append(b.RouteSignals, workerenv.Signal{Name: name, Layer: layer})
}

// claudeRoute records the route of a successful `claude auth status --json`.
// Only claude.ai and oauth_token logins stay subscription candidates; when
// the reply names an apiProvider, only firstParty does.
func (b *BillingFacts) claudeRoute(method, provider *string) {
	b.RouteKnown = true
	if method == nil {
		b.RouteKnown = false
		b.signal("auth:unknown", claudeAuthLayer)
	} else {
		b.AuthMethod = known(*method, "none", "claude.ai", "oauth_token", "api_key", "api_key_helper", "third_party")
		switch b.AuthMethod {
		case "claude.ai", "oauth_token":
		case "unknown":
			b.RouteKnown = false
			b.signal("auth:unknown", claudeAuthLayer)
		default:
			b.signal("auth:"+b.AuthMethod, claudeAuthLayer)
		}
	}
	if provider != nil {
		b.APIProvider = known(*provider, "firstParty", "bedrock", "vertex", "foundry", "anthropicAws", "anthropicGoogleCloud", "mantle", "gateway")
		if b.APIProvider == "unknown" {
			b.RouteKnown = false
		}
		if b.APIProvider != "firstParty" {
			b.signal("auth:apiProvider", claudeAuthLayer)
		}
	}
}

// claudeRouteUnread records a failed auth status read: missing route
// evidence, never a first-party login.
func (b *BillingFacts) claudeRouteUnread() {
	b.AuthMethod, b.APIProvider, b.RouteKnown = "", "", false
	b.signal("auth:unread", claudeAuthLayer)
}

// addClaudeWorkspaceReason keeps the level of an overage disabled reason set
// at org, seat tier, group or member level. Other reasons are not workspace
// evidence and are dropped.
func (b *BillingFacts) addClaudeWorkspaceReason(reason string) {
	category := ""
	switch reason {
	case "org_level_disabled", "org_level_disabled_until", "org_service_level_disabled":
		category = "org"
	case "seat_tier_level_disabled", "seat_tier_zero_credit_limit":
		category = "seat_tier"
	case "group_zero_credit_limit":
		category = "group"
	case "member_level_disabled", "member_zero_credit_limit":
		category = "member"
	}
	if category != "" && !slices.Contains(b.WorkspaceReasons, category) {
		b.WorkspaceReasons = append(b.WorkspaceReasons, category)
	}
}

func known(value string, spellings ...string) string {
	if slices.Contains(spellings, value) {
		return value
	}
	return "unknown"
}
