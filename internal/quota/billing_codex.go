package quota

import (
	"cmp"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/workerenv"
)

const (
	codexAccountLayer = "account/read"
	codexConfigLayer  = "config/read"
	codexAuthLayer    = "getAuthStatus"
)

// codexWorkspacePlans are the Codex plan types that belong to a workspace.
// A plan type is workspace evidence only; it never decides the verdict.
var codexWorkspacePlans = []string{"team", "business", "enterprise", "edu"}

// workspacePlan reports a plan type of a workspace under any spelling of f.
func (f fields) workspacePlan() bool {
	kind, _ := f.text("planType", "plan_type")
	return slices.Contains(codexWorkspacePlans, strings.ToLower(kind))
}

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

// identity returns the first spelling that holds a string, as bytes for the
// identity sink; nil when none does or the string is empty.
func (f fields) identity(names ...string) []byte {
	if v, ok := f.text(names...); ok && v != "" {
		return []byte(v)
	}
	return nil
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
// The email, plan and workspace name are never kept; a workspace name or a
// workspace plan type only counts as workspace evidence. A workspaceRouting
// object is no evidence: a personal account has one too.
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
		if account.workspacePlan() {
			b.addWorkspaceReason("workspace_plan")
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

// codexIdentity returns the transient identity fields of `account/read`: the
// account's email and its workspace name. They go to the identity sink only.
func codexIdentity(data []byte) (account, org []byte) {
	var reply fields
	_ = json.Unmarshal(data, &reply)
	a := reply.object("account")
	return a.identity("email"), a.identity("workspaceName", "workspace_name")
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// value returns the raw value of the first spelling present.
func (f fields) value(names ...string) (json.RawMessage, bool) {
	for _, name := range names {
		if raw, ok := f[name]; ok {
			return raw, true
		}
	}
	return nil, false
}

// codexBuiltinProvider is the id of Codex's built-in OpenAI provider, the
// only one a subscription login runs on.
const codexBuiltinProvider = "openai"

// parseCodexConfig reads the provider route of `config/read`; Codex 0.165
// has no provider list. The provider is the selected profile's
// model_provider when the profile sets one, else the config's; null means
// the built-in OpenAI provider. That provider stays a subscription
// candidate only while no model_providers entry under its id sets a key, a
// token, headers, an auth command, AWS settings, a base URL or turns OpenAI
// auth off. Any other provider is not first party. The provider, providers
// and profile keys must be in the reply: a version without them, or a value
// of another type, leaves the route unknown. Of an env_key only a valid
// variable name is kept, and a provider id is never kept. A forced ChatGPT
// workspace id is workspace evidence.
func parseCodexConfig(data []byte) (BillingFacts, error) {
	b := BillingFacts{RouteKnown: true}
	unknown := func() (BillingFacts, error) {
		b.unknownRoute("provider:unknown", codexConfigLayer)
		return b, errors.New("unknown config reply")
	}
	var reply fields
	_ = json.Unmarshal(data, &reply)
	config := reply.object("config")
	providerKey := []string{"model_provider", "modelProvider"}
	providersKey := []string{"model_providers", "modelProviders"}
	if config == nil || !config.has(providerKey...) || !config.has(providersKey...) || !config.has("profile") {
		return unknown()
	}
	if raw, ok := config.value("forced_chatgpt_workspace_id", "forcedChatgptWorkspaceId"); ok && string(raw) != "null" && string(raw) != `""` {
		b.addWorkspaceReason("forced_workspace")
	}
	selected := config
	if raw := config["profile"]; string(raw) != "null" {
		name, ok := config.text("profile")
		profile := config.object("profiles").object(name)
		if !ok || profile == nil {
			return unknown()
		}
		if profile.has(providerKey...) {
			selected = profile
		}
	}
	id := codexBuiltinProvider
	if raw, _ := selected.value(providerKey...); string(raw) != "null" {
		var ok bool
		if id, ok = selected.text(providerKey...); !ok || id == "" {
			return unknown()
		}
	}
	providers := config.object(providersKey...)
	if raw, _ := config.value(providersKey...); providers == nil && string(raw) != "null" {
		return unknown()
	}
	if id != codexBuiltinProvider {
		b.signal("provider:custom", codexConfigLayer)
	}
	entry := providers.object(id)
	if entry == nil {
		return b, nil
	}
	if requires := entry.boolean("requires_openai_auth", "requiresOpenaiAuth"); requires != nil && !*requires {
		b.signal("provider:requires_openai_auth", codexConfigLayer)
	}
	envKey := []string{"env_key", "envKey"}
	if entry.set(envKey...) {
		b.signal("provider:env_key", codexConfigLayer)
		if key, ok := entry.text(envKey...); ok && envName.MatchString(key) {
			b.ProviderEnvKey = key
		}
	}
	for signal, names := range map[string][]string{
		"provider:bearer_token": {"experimental_bearer_token", "experimentalBearerToken"},
		"provider:headers":      {"http_headers", "httpHeaders", "env_http_headers", "envHttpHeaders"},
		"provider:auth":         {"auth"},
		"provider:aws":          {"aws"},
	} {
		if entry.set(names...) {
			b.signal(signal, codexConfigLayer)
		}
	}
	if raw, ok := entry.value("base_url", "baseUrl"); ok && string(raw) != "null" {
		b.signal("provider:base_url", codexConfigLayer)
	}
	slices.SortStableFunc(b.RouteSignals, func(x, y workerenv.Signal) int { return strings.Compare(x.Name, y.Name) })
	return b, nil
}

// parseCodexAuth reads the stored login of `getAuthStatus`. Only a chatgpt
// login stays a subscription candidate; an API key or ChatGPT auth tokens
// are paid, no login is none, and a missing or new method leaves the route
// unknown. duolog never asks for the token, and never keeps one.
func parseCodexAuth(data []byte) (BillingFacts, error) {
	b := BillingFacts{RouteKnown: true}
	var reply fields
	_ = json.Unmarshal(data, &reply)
	raw, ok := reply.value("authMethod", "auth_method")
	if !ok {
		b.unknownRoute("auth:unknown", codexAuthLayer)
		return b, errors.New("unknown auth reply")
	}
	if string(raw) == "null" {
		b.AuthMethod = "none"
		b.signal("auth:none", codexAuthLayer)
		return b, nil
	}
	method, _ := reply.text("authMethod", "auth_method")
	switch strings.ToLower(method) {
	case "chatgpt":
		b.AuthMethod = "chatgpt"
	case "apikey":
		b.AuthMethod = "apiKey"
	case "chatgptauthtokens":
		b.AuthMethod = "chatgptAuthTokens"
	default:
		b.AuthMethod = "unknown"
		b.unknownRoute("auth:unknown", codexAuthLayer)
		return b, nil
	}
	if b.AuthMethod != "chatgpt" {
		b.signal("auth:"+b.AuthMethod, codexAuthLayer)
	}
	return b, nil
}

// codexBilling joins the route facts of `account/read`, `config/read` and
// `getAuthStatus` with the window facts of the rate limit read. The route is
// known only when every route read named one; the window read never clears
// a route signal.
func codexBilling(windows BillingFacts, routes ...BillingFacts) BillingFacts {
	b := windows
	b.RouteKnown = len(routes) > 0
	b.RouteSignals, b.WorkspaceReasons = nil, nil
	for _, r := range routes {
		b.RouteKnown = b.RouteKnown && r.RouteKnown
		b.AccountType = cmp.Or(b.AccountType, r.AccountType)
		b.AuthMode = cmp.Or(b.AuthMode, r.AuthMode)
		b.AuthMethod = cmp.Or(b.AuthMethod, r.AuthMethod)
		b.ProviderEnvKey = cmp.Or(b.ProviderEnvKey, r.ProviderEnvKey)
		b.RouteSignals = append(b.RouteSignals, r.RouteSignals...)
		for _, category := range r.WorkspaceReasons {
			b.addWorkspaceReason(category)
		}
	}
	b.RouteSignals = append(b.RouteSignals, windows.RouteSignals...)
	for _, category := range windows.WorkspaceReasons {
		b.addWorkspaceReason(category)
	}
	return b
}

var codexOrdinaryUsage = []string{"ordinaryUsageAllowed", "ordinary_usage_allowed"}

// codexPermission is one ordinary usage flag of a reply: seen says the reply
// had the key, value is nil for null or a wrong type.
type codexPermission struct {
	value *bool
	seen  bool
}

func readCodexPermission(f fields) codexPermission {
	return codexPermission{value: f.boolean(codexOrdinaryUsage...), seen: f.has(codexOrdinaryUsage...)}
}

// joinPermissions keeps the strictest flag sent: false wins, then null or a
// wrong type, which give nil; true needs a flag and no other value. No flag
// at all is nil.
func joinPermissions(flags ...codexPermission) *bool {
	allowed, null := false, false
	for _, p := range flags {
		switch {
		case !p.seen:
		case p.value == nil:
			null = true
		case !*p.value:
			v := false
			return &v
		default:
			allowed = true
		}
	}
	if null || !allowed {
		return nil
	}
	v := true
	return &v
}

// codexLimitFacts are the permission and spend properties of one rate limit
// as the reply sent them, under either spelling. Absent, null and a wrong
// type stay apart from an explicit true.
type codexLimitFacts struct {
	ordinary      codexPermission
	spendPresent  bool
	spendReached  bool
	workspacePlan bool
	reachedType   json.RawMessage
	credits       bool
	resetCredits  *int
}

func readCodexLimitFacts(f fields) codexLimitFacts {
	l := codexLimitFacts{ordinary: readCodexPermission(f), workspacePlan: f.workspacePlan()}
	// A spend control key that is there, even false, is only a fact: a
	// personal account sends spendControlReached false too.
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
// reached spend control turn overage on, and a reached spend control, a
// workspace plan type or a workspace reached type is workspace evidence. A
// spend control that is there but not reached is neither.
func (b *BillingFacts) addCodexLimit(l codexLimitFacts) {
	b.SpendControlPresent = b.SpendControlPresent || l.spendPresent
	b.OverageEnabled = b.OverageEnabled || l.credits || l.spendReached
	if l.spendReached {
		b.addWorkspaceReason("spend_control")
	}
	if l.workspacePlan {
		b.addWorkspaceReason("workspace_plan")
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
