package quota

import (
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
)

const (
	codexAccountLayer  = "account/read"
	codexProviderLayer = "modelProvider/list"
)

// fields keeps a reply object's members raw, so that a property can be told
// apart as absent, null or set under any of its spellings.
type fields map[string]json.RawMessage

func (f fields) has(names ...string) bool {
	return slices.ContainsFunc(names, func(name string) bool { _, ok := f[name]; return ok })
}

// set reports a property present under some spelling as neither null nor
// false. Any other value counts, even an empty one.
func (f fields) set(names ...string) bool {
	return slices.ContainsFunc(names, func(name string) bool {
		raw, ok := f[name]
		return ok && string(raw) != "null" && string(raw) != "false"
	})
}

// boolean reads a flag under its spellings. It is true only when every
// spelling present says true: false wins, then null or a wrong type, which
// both give nil.
func (f fields) boolean(names ...string) *bool {
	state := 0 // absent, true, null or wrong type, false
	for _, name := range names {
		raw, ok := f[name]
		if !ok {
			continue
		}
		var v *bool
		s := 2
		if json.Unmarshal(raw, &v) == nil && v != nil {
			s = 1
			if !*v {
				s = 3
			}
		}
		state = max(state, s)
	}
	switch state {
	case 1:
		v := true
		return &v
	case 3:
		v := false
		return &v
	}
	return nil
}

// text returns the first spelling that holds a string.
func (f fields) text(names ...string) (string, bool) {
	for _, name := range names {
		var v *string
		if json.Unmarshal(f[name], &v) == nil && v != nil {
			return *v, true
		}
	}
	return "", false
}

func (f fields) object(names ...string) fields {
	for _, name := range names {
		var o fields
		if json.Unmarshal(f[name], &o) == nil && o != nil {
			return o
		}
	}
	return nil
}

// unreadFacts records a free read that failed: missing route evidence, never
// a first-party route.
func unreadFacts(name, layer string) BillingFacts {
	b := BillingFacts{}
	b.unknownRoute(name, layer)
	return b
}

// parseCodexAccount reads the route evidence of `account/read`. Only a
// chatgpt account adds no signal, and even that proves nothing alone: the
// stored login says nothing about where the configured provider sends runs.
// The email, plan and workspace name are never kept; a workspace name only
// counts as workspace evidence.
func parseCodexAccount(data []byte) (BillingFacts, error) {
	b := BillingFacts{RouteKnown: true}
	var reply fields
	if json.Unmarshal(data, &reply) != nil || reply == nil || !reply.has("account") {
		b.unknownRoute("account:unknown", codexAccountLayer)
		return b, errors.New("unknown account reply")
	}
	account := reply.object("account")
	switch {
	case string(reply["account"]) == "null":
		b.AccountType = "none"
		b.signal("account:none", codexAccountLayer)
	case account == nil:
		b.unknownRoute("account:unknown", codexAccountLayer)
	default:
		kind, _ := account.text("type")
		b.AccountType = known(kind, "apiKey", "chatgpt", "amazonBedrock", "gcp")
		switch b.AccountType {
		case "chatgpt":
		case "unknown":
			b.unknownRoute("account:unknown", codexAccountLayer)
		default:
			b.signal("account:"+b.AccountType, codexAccountLayer)
		}
		if name, ok := account.text("workspaceName", "workspace_name"); ok && name != "" {
			b.addWorkspaceReason("workspace_name")
		}
	}
	if reply.has("requiresOpenaiAuth", "requires_openai_auth") {
		if requires := reply.boolean("requiresOpenaiAuth", "requires_openai_auth"); requires == nil {
			b.unknownRoute("account:unknown", codexAccountLayer)
		} else if !*requires {
			b.signal("account:requires_openai_auth", codexAccountLayer)
		}
	}
	// Auth modes with unconfirmed billing, such as headers or agentIdentity,
	// are paid; only a plain chatgpt login stays a candidate.
	for _, source := range []fields{reply, account} {
		if !source.set("authMode", "auth_mode") {
			continue
		}
		mode, _ := source.text("authMode", "auth_mode")
		b.AuthMode = known(mode, "apiKey", "chatgpt", "chatgptAuthTokens", "headers", "agentIdentity", "personalAccessToken", "bedrockApiKey", "bedrockAccessKeys")
		switch b.AuthMode {
		case "chatgpt":
		case "unknown":
			b.unknownRoute("auth:unknown", codexAccountLayer)
		default:
			b.signal("auth:"+b.AuthMode, codexAccountLayer)
		}
		break
	}
	return b, nil
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// parseCodexProvider applies Codex's own first-party test to the default
// provider of `modelProvider/list`: it requires OpenAI auth, has no env_key,
// bearer token, auth command or AWS settings, and has a null baseUrl. Every
// property must be in the reply; a version that lacks one leaves the route
// unknown, because ids and kinds decide nothing. Of the env_key only a valid
// variable name is kept.
func parseCodexProvider(data []byte) (BillingFacts, error) {
	b := BillingFacts{RouteKnown: true}
	var reply fields
	_ = json.Unmarshal(data, &reply)
	id, _ := reply.text("defaultProviderId", "default_provider_id")
	var provider fields
	for _, entry := range slices.Concat(providerList(reply["data"]), providerList(reply["providers"])) {
		if entryID, ok := entry.text("id"); ok && id != "" && entryID == id {
			provider = entry
			break
		}
	}
	requires := provider.boolean("requiresOpenaiAuth", "requires_openai_auth")
	envKey := []string{"envKey", "env_key"}
	bearer := []string{"experimentalBearerToken", "experimental_bearer_token"}
	baseURL := []string{"baseUrl", "base_url"}
	if provider == nil || requires == nil || !provider.has(envKey...) || !provider.has(bearer...) ||
		!provider.has("auth") || !provider.has("aws") || !provider.has(baseURL...) {
		b.unknownRoute("provider:unknown", codexProviderLayer)
		return b, errors.New("unknown provider reply")
	}
	if !*requires {
		b.signal("provider:requires_openai_auth", codexProviderLayer)
	}
	if provider.set(envKey...) {
		b.signal("provider:env_key", codexProviderLayer)
		for _, name := range envKey {
			if key, ok := provider.text(name); ok && envName.MatchString(key) {
				b.ProviderEnvKey = key
				break
			}
		}
	}
	if provider.set(bearer...) {
		b.signal("provider:bearer_token", codexProviderLayer)
	}
	if provider.set("auth") {
		b.signal("provider:auth", codexProviderLayer)
	}
	if provider.set("aws") {
		b.signal("provider:aws", codexProviderLayer)
	}
	if slices.ContainsFunc(baseURL, func(name string) bool { raw, ok := provider[name]; return ok && string(raw) != "null" }) {
		b.signal("provider:base_url", codexProviderLayer)
	}
	return b, nil
}

func providerList(raw json.RawMessage) []fields {
	var list []fields
	if json.Unmarshal(raw, &list) != nil {
		return nil
	}
	return list
}

// codexBilling joins the route facts of `account/read` and
// `modelProvider/list` with the window facts of the rate limit read. The
// route is known only when both reads named one; the window read never
// clears a route signal.
func codexBilling(account, provider, windows BillingFacts) BillingFacts {
	b := windows
	b.AccountType, b.AuthMode, b.ProviderEnvKey = account.AccountType, account.AuthMode, provider.ProviderEnvKey
	b.RouteKnown = account.RouteKnown && provider.RouteKnown
	b.RouteSignals = slices.Concat(account.RouteSignals, provider.RouteSignals, windows.RouteSignals)
	b.WorkspaceReasons = nil
	for _, category := range slices.Concat(account.WorkspaceReasons, windows.WorkspaceReasons) {
		b.addWorkspaceReason(category)
	}
	return b
}

// codexLimitFacts are the permission and spend properties of one rate limit
// as the reply sent them, under either spelling. Absent, null and a wrong
// type stay apart from an explicit true.
type codexLimitFacts struct {
	ordinary     *bool
	spendPresent bool
	spendReached bool
	reachedType  json.RawMessage
	credits      bool
	resetCredits *int
}

func readCodexLimitFacts(f fields) codexLimitFacts {
	l := codexLimitFacts{ordinary: f.boolean("ordinaryUsageAllowed", "ordinary_usage_allowed")}
	// A spend control that is present at all, even false or zero, is one.
	l.spendPresent = slices.ContainsFunc([]string{"spendControlReached", "spend_control_reached", "spendControl", "spend_control"}, func(name string) bool {
		raw, ok := f[name]
		return ok && string(raw) != "null"
	})
	l.spendReached = string(f["spendControlReached"]) == "true" || string(f["spend_control_reached"]) == "true"
	for _, name := range []string{"rateLimitReachedType", "rate_limit_reached_type"} {
		if raw, ok := f[name]; ok && string(raw) != "null" {
			l.reachedType = raw
			break
		}
	}
	credits := f.object("credits")
	for _, flag := range []*bool{credits.boolean("hasCredits", "has_credits"), credits.boolean("unlimited")} {
		l.credits = l.credits || (flag != nil && *flag)
	}
	for _, name := range []string{"resetCredits", "reset_credits"} {
		var n *int
		if json.Unmarshal(f[name], &n) == nil && n != nil && *n >= 0 {
			l.resetCredits = n
			break
		}
	}
	return l
}

// addCodexLimit keeps the account-wide evidence of one limit: credits or a
// spend control turn overage on, and a spend control or a workspace reached
// type is workspace evidence.
func (b *BillingFacts) addCodexLimit(l codexLimitFacts) {
	b.SpendControlPresent = b.SpendControlPresent || l.spendPresent
	b.OverageEnabled = b.OverageEnabled || l.credits || l.spendPresent
	if l.spendPresent {
		b.addWorkspaceReason("spend_control")
	}
	var reached string
	if json.Unmarshal(l.reachedType, &reached) == nil {
		switch strings.ToLower(strings.ReplaceAll(reached, "_", "")) {
		case "workspaceownercreditsdepleted":
			b.addWorkspaceReason("workspace_owner_credits_depleted")
		case "workspacemembercreditsdepleted":
			b.addWorkspaceReason("workspace_member_credits_depleted")
		case "workspaceownerusagelimitreached":
			b.addWorkspaceReason("workspace_owner_usage_limit_reached")
		case "workspacememberusagelimitreached":
			b.addWorkspaceReason("workspace_member_usage_limit_reached")
		}
	}
}
