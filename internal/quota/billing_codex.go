package quota

import (
	"cmp"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

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
	unknownRoute(&b, name, layer)
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
		unknownRoute(&b, "account:unknown", codexAccountLayer)
		return b, errors.New("unknown account reply")
	}
	account := reply.object("account")
	switch {
	case string(reply["account"]) == "null":
		b.AccountType = "none"
		addSignal(&b, "account:none", codexAccountLayer)
	case account == nil:
		unknownRoute(&b, "account:unknown", codexAccountLayer)
	default:
		kind, _ := account.text("type")
		b.AccountType = known(kind, "apiKey", "chatgpt", "amazonBedrock", "gcp")
		switch b.AccountType {
		case "chatgpt":
		case "unknown":
			unknownRoute(&b, "account:unknown", codexAccountLayer)
		default:
			addSignal(&b, "account:"+b.AccountType, codexAccountLayer)
		}
		if name, ok := account.text("workspaceName", "workspace_name"); ok && name != "" {
			addWorkspaceReason(&b, "workspace_name")
		}
		if account.workspacePlan() {
			addWorkspaceReason(&b, "workspace_plan")
		}
	}
	if reply.has("requiresOpenaiAuth", "requires_openai_auth") {
		if requires := reply.boolean("requiresOpenaiAuth", "requires_openai_auth"); requires == nil {
			unknownRoute(&b, "account:unknown", codexAccountLayer)
		} else if !*requires {
			addSignal(&b, "account:requires_openai_auth", codexAccountLayer)
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
			unknownRoute(&b, "auth:unknown", codexAccountLayer)
		default:
			addSignal(&b, "auth:"+b.AuthMode, codexAccountLayer)
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
		unknownRoute(&b, "provider:unknown", codexConfigLayer)
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
		addWorkspaceReason(&b, "forced_workspace")
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
		addSignal(&b, "provider:custom", codexConfigLayer)
	}
	entry := providers.object(id)
	if entry == nil {
		return b, nil
	}
	if requires := entry.boolean("requires_openai_auth", "requiresOpenaiAuth"); requires != nil && !*requires {
		addSignal(&b, "provider:requires_openai_auth", codexConfigLayer)
	}
	envKey := []string{"env_key", "envKey"}
	if entry.set(envKey...) {
		addSignal(&b, "provider:env_key", codexConfigLayer)
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
			addSignal(&b, signal, codexConfigLayer)
		}
	}
	if raw, ok := entry.value("base_url", "baseUrl"); ok && string(raw) != "null" {
		addSignal(&b, "provider:base_url", codexConfigLayer)
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
		unknownRoute(&b, "auth:unknown", codexAuthLayer)
		return b, errors.New("unknown auth reply")
	}
	if string(raw) == "null" {
		b.AuthMethod = "none"
		addSignal(&b, "auth:none", codexAuthLayer)
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
		unknownRoute(&b, "auth:unknown", codexAuthLayer)
		return b, nil
	}
	if b.AuthMethod != "chatgpt" {
		addSignal(&b, "auth:"+b.AuthMethod, codexAuthLayer)
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
			addWorkspaceReason(&b, category)
		}
	}
	b.RouteSignals = append(b.RouteSignals, windows.RouteSignals...)
	for _, category := range windows.WorkspaceReasons {
		addWorkspaceReason(&b, category)
	}
	return b
}

var codexOrdinaryUsage = []string{"ordinaryUsageAllowed", "ordinary_usage_allowed"}

// codexPermission is one ordinary usage flag of a reply: seen says the reply
// had the key, value is nil for null or a wrong type, and null says that every
// spelling sent was null.
type codexPermission struct {
	value *bool
	seen  bool
	null  bool
}

func readCodexPermission(f fields) codexPermission {
	null := f.has(codexOrdinaryUsage...) && !slices.ContainsFunc(codexOrdinaryUsage, func(name string) bool {
		raw, ok := f[name]
		return ok && string(raw) != "null"
	})
	return codexPermission{value: f.boolean(codexOrdinaryUsage...), seen: f.has(codexOrdinaryUsage...), null: null}
}

// permissionNull reports a permission that the reply sent, and sent as null
// wherever it sent one.
func permissionNull(flags ...codexPermission) bool {
	seen := false
	for _, p := range flags {
		if p.seen && !p.null {
			return false
		}
		seen = seen || p.seen
	}
	return seen
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
	// plan is the limit's plan name as sent, under either spelling.
	plan string
	// individual is the limit's individualLimit, nil when absent or null.
	individual *codexIndividual
}

func readCodexLimitFacts(f fields) codexLimitFacts {
	l := codexLimitFacts{ordinary: readCodexPermission(f), workspacePlan: f.workspacePlan()}
	l.plan, _ = f.text("planType", "plan_type")
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
	if o := f.object("individualLimit", "individual_limit"); o != nil {
		l.individual = &codexIndividual{o}
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
func addCodexLimit(b *BillingFacts, l codexLimitFacts) {
	b.SpendControlPresent = b.SpendControlPresent || l.spendPresent
	b.OverageEnabled = b.OverageEnabled || l.credits || l.spendReached
	if l.spendReached {
		addWorkspaceReason(b, "spend_control")
	}
	if l.workspacePlan {
		addWorkspaceReason(b, "workspace_plan")
	}
	var reached string
	if json.Unmarshal(l.reachedType, &reached) == nil {
		switch strings.ToLower(strings.ReplaceAll(reached, "_", "")) {
		case "workspaceownercreditsdepleted":
			addWorkspaceReason(b, "workspace_owner_credits_depleted")
		case "workspacemembercreditsdepleted":
			addWorkspaceReason(b, "workspace_member_credits_depleted")
		case "workspaceownerusagelimitreached":
			addWorkspaceReason(b, "workspace_owner_usage_limit_reached")
		case "workspacememberusagelimitreached":
			addWorkspaceReason(b, "workspace_member_usage_limit_reached")
		}
	}
}

// number reads the first spelling that holds a finite number, sent as a
// JSON number or as a string such as "400.00".
func (f fields) number(names ...string) (float64, bool) {
	for _, name := range names {
		var n json.Number
		if json.Unmarshal(f[name], &n) != nil || n == "" {
			continue
		}
		if v, err := strconv.ParseFloat(string(n), 64); err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) {
			return v, true
		}
	}
	return 0, false
}

// codexIndividualKind is the window kind of an individual limit.
const codexIndividualKind = "individual"

// codexIndividual is a rate limit's individualLimit: the per-person spend
// limit of an enterprise or workspace seat, which Codex 0.166 sends instead
// of primary and secondary windows. limit and used are amounts in credits,
// often as strings; remainingPercent and resetsAt come beside them.
type codexIndividual struct{ f fields }

// window turns the individual limit into a window of unknown length, at the
// higher of used over limit, when both are numbers and the limit is above 0,
// and 100 minus remainingPercent. Spend past the limit counts as full. A
// negative amount, a remaining share outside 0 to 100 or no usable value is an
// invalid window, as for primary and secondary. Its length stays 0, so the window
// gets the stricter terms of a window of unknown length; a reset at or
// before the read makes it full or no window, as for any other window.
func (c codexIndividual) window() (Window, error) {
	percent, err := c.usedPercent()
	if err != nil {
		return Window{}, err
	}
	// Two decimals keep duolog quota readable; the error is below 0.005
	// points of the window.
	w := Window{Kind: codexIndividualKind, UsedPercent: math.Round(percent*100) / 100}
	// Seconds since 1970; a value past year 33000 is no reset time.
	if at, ok := c.f.number("resetsAt", "resets_at"); ok && at > 0 && at < 1e12 {
		w.ResetsAt = time.Unix(int64(at), 0).UTC()
	}
	return w, nil
}

// usedPercent is the share of the limit used: the higher of what limit and
// used say, when the limit is above zero, and what remainingPercent says.
func (c codexIndividual) usedPercent() (float64, error) {
	invalid := errors.New("invalid quota window")
	remaining, hasRemaining := c.f.number("remainingPercent", "remaining_percent")
	if hasRemaining && (remaining < 0 || remaining > 100) {
		return 0, invalid
	}
	limit, hasLimit := c.f.number("limit")
	used, hasUsed := c.f.number("used")
	if hasLimit && limit < 0 || hasUsed && used < 0 {
		return 0, invalid
	}
	percent, known := 0.0, false
	if hasLimit && hasUsed && limit > 0 {
		percent, known = min(used/limit*100, 100), true
	}
	if hasRemaining {
		percent, known = max(percent, 100-remaining), true
	}
	if !known {
		return 0, invalid
	}
	return percent, nil
}

// open reports an individual limit with room left: a positive numeric limit, a
// numeric used, a reset time, below 100% used, and a remainingPercent, when
// sent, above 0. Whether the reset lies ahead is the verdict's check, at its
// own time: a window past its reset is no window, which is paid. A nil limit
// is not open.
func (c *codexIndividual) open() bool {
	if c == nil {
		return false
	}
	limit, hasLimit := c.f.number("limit")
	if _, hasUsed := c.f.number("used"); !hasLimit || !hasUsed || limit <= 0 {
		return false
	}
	w, err := c.window()
	if err != nil || w.ResetsAt.IsZero() || w.UsedPercent >= 100 {
		return false
	}
	remaining, ok := c.f.number("remainingPercent", "remaining_percent")
	return !ok || remaining > 0
}

// codexPersonalPlans are the plan names Codex gives a person's own account.
var codexPersonalPlans = []string{"free", "go", "plus", "pro", "max", "ultra"}

// codexByUseWords name billing by use, as the words a peer's pricing question
// is judged by (internal/app/peer/paid_runs.go), with _ and - read as spaces.
var codexByUseWords = []string{"usage based", "pay as you go", "extra usage"}

func codexPersonalPlan(plan string) bool {
	return slices.Contains(codexPersonalPlans, strings.ToLower(plan))
}

// codexSeatPlan reports a plan name that may be a seat: one that is there, not
// a person's own plan, and not one that names billing by use. Unknown names
// may be: Codex renames its enterprise plans, as ent26 shows.
func codexSeatPlan(plan string) bool {
	name := strings.NewReplacer("_", " ", "-", " ").Replace(strings.ToLower(plan))
	return strings.TrimSpace(plan) != "" && !codexPersonalPlan(plan) && !slices.ContainsFunc(codexByUseWords, func(word string) bool { return strings.Contains(name, word) })
}

// refusesSeat reports a limit that names a plan no seat has. A limit of one
// model names no plan in Codex's replies, and says nothing about the seat.
func (l codexLimit) refusesSeat() bool {
	return l.facts.plan != "" && !codexSeatPlan(l.facts.plan)
}

// allowIndividualSeat treats an enterprise or workspace seat on a per-person
// spend limit as a subscription, as the project chose: duolog must work with
// such a seat by default. As Codex 0.166 reports one, it sends
// ordinaryUsageAllowed null and no primary or secondary window, and its
// individualLimit is the permission. An open limit is workspace evidence,
// unless the plan is a person's own, so an unknown plan name never reads as a
// personal account.
//
// Only that exact shape turns no permission into one: the flag present and
// null, no primary or secondary window, an open limit, a seat plan on every
// limit, and nothing that refuses runs. Any other shape keeps an unknown
// permission, which the verdict counts as paid. A false from any entry of the
// reply wins over the grant, even one with other numbers. A scoped limit never
// grants. readCodex settles the route and the account's plan afterwards.
func allowIndividualSeat(s Snapshot, l *codexLimit, top codexPermission, byID map[string]codexLimit) Snapshot {
	if l.facts.individual.open() && !codexPersonalPlan(l.facts.plan) {
		addWorkspaceReason(&s.Billing, "workspace_plan")
	}
	if s.Billing.OrdinaryUsageAllowed != nil || l.Primary != nil || l.Secondary != nil || !l.facts.individual.open() || s.Rejected || s.SpendControlReached || !permissionNull(top, l.facts.ordinary) {
		return s
	}
	limits := slices.Collect(maps.Values(byID))
	if slices.ContainsFunc(limits, func(x codexLimit) bool { return x.facts.ordinary.value != nil && !*x.facts.ordinary.value }) {
		refused := false
		s.Billing.OrdinaryUsageAllowed, s.Rejected = &refused, true
		return s
	}
	if !codexSeatPlan(l.facts.plan) || slices.ContainsFunc(limits, codexLimit.refusesSeat) {
		return s
	}
	allowed := true
	s.Billing.OrdinaryUsageAllowed = &allowed
	return s
}

// settleCodexSeat takes back a seat grant that the account read rules out: a
// route other than a chatgpt account, or a plan on the account that is no
// seat's. The rate limit read alone cannot say. A permission the reply sent as
// true is no grant and stays.
func settleCodexSeat(b BillingFacts, rateLimits []byte, account BillingFacts, plan string) BillingFacts {
	if b.OrdinaryUsageAllowed == nil || !*b.OrdinaryUsageAllowed || account.AccountType == "chatgpt" && codexSeatPlan(plan) {
		return b
	}
	var reply fields
	if json.Unmarshal(rateLimits, &reply) == nil && permissionNull(readCodexPermission(reply), readCodexPermission(reply.object("rateLimits"))) {
		b.OrdinaryUsageAllowed = nil
	}
	return b
}

// readCodexAccount is parseCodexAccount with the account's plan name, which
// only the seat check reads and which is never kept.
func readCodexAccount(data []byte) (BillingFacts, string) {
	b, _ := parseCodexAccount(data)
	var reply fields
	_ = json.Unmarshal(data, &reply)
	plan, _ := reply.object("account").text("planType", "plan_type")
	return b, plan
}
