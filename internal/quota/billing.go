package quota

import (
	"slices"

	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// BillingFacts is the billing evidence of a free read; core/account holds the
// type, and the functions below fill it from a reply.
type BillingFacts = account.BillingFacts

const claudeAuthLayer = "auth status"

func addSignal(b *BillingFacts, name, layer string) {
	b.RouteSignals = append(b.RouteSignals, workerenv.Signal{Name: name, Layer: layer})
}

// unknownRoute marks the route unknown and names the signal once.
func unknownRoute(b *BillingFacts, name, layer string) {
	b.RouteKnown = false
	if !slices.Contains(b.RouteSignals, workerenv.Signal{Name: name, Layer: layer}) {
		addSignal(b, name, layer)
	}
}

func addWorkspaceReason(b *BillingFacts, category string) {
	if category != "" && !slices.Contains(b.WorkspaceReasons, category) {
		b.WorkspaceReasons = append(b.WorkspaceReasons, category)
	}
}

// claudeRoute records the route of a successful `claude auth status --json`.
// Only claude.ai and oauth_token logins stay subscription candidates; when
// the reply names an apiProvider, only firstParty does.
func claudeRoute(b *BillingFacts, method, provider *string) {
	b.RouteKnown = true
	if method == nil {
		b.RouteKnown = false
		addSignal(b, "auth:unknown", claudeAuthLayer)
	} else {
		b.AuthMethod = known(*method, "none", "claude.ai", "oauth_token", "api_key", "api_key_helper", "third_party")
		switch b.AuthMethod {
		case "claude.ai", "oauth_token":
		case "unknown":
			b.RouteKnown = false
			addSignal(b, "auth:unknown", claudeAuthLayer)
		default:
			addSignal(b, "auth:"+b.AuthMethod, claudeAuthLayer)
		}
	}
	if provider != nil {
		b.APIProvider = known(*provider, "firstParty", "bedrock", "vertex", "foundry", "anthropicAws", "anthropicGoogleCloud", "mantle", "gateway")
		if b.APIProvider == "unknown" {
			b.RouteKnown = false
		}
		if b.APIProvider != "firstParty" {
			addSignal(b, "auth:apiProvider", claudeAuthLayer)
		}
	}
}

// claudeRouteUnread records a failed auth status read: missing route
// evidence, never a first-party login.
func claudeRouteUnread(b *BillingFacts) {
	b.AuthMethod, b.APIProvider, b.RouteKnown = "", "", false
	addSignal(b, "auth:unread", claudeAuthLayer)
}

// claudeRouteTimedOut records an auth status read that did not end in time:
// the route is unknown, which is paid like any unknown, and the signal says
// so rather than that the login could not be read.
func claudeRouteTimedOut(b *BillingFacts) {
	b.AuthMethod, b.APIProvider, b.RouteKnown = "", "", false
	addSignal(b, authTimeoutSignal, claudeAuthLayer)
}

const authTimeoutSignal = "auth:timeout"

// timedOut reports whether auth status ran past its bound.
func timedOut(b BillingFacts) bool {
	return slices.Contains(b.RouteSignals, workerenv.Signal{Name: authTimeoutSignal, Layer: claudeAuthLayer})
}

// addClaudeWorkspaceReason keeps the level of an overage disabled reason set
// at org, seat tier, group or member level. Other reasons are not workspace
// evidence and are dropped.
func addClaudeWorkspaceReason(b *BillingFacts, reason string) {
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
	addWorkspaceReason(b, category)
}

func known(value string, spellings ...string) string {
	if slices.Contains(spellings, value) {
		return value
	}
	return "unknown"
}
