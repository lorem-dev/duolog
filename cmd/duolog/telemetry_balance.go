package main

import (
	"maps"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/core/record"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/router"
	"github.com/lorem-dev/duolog/internal/telemetry"
)

// balanceObserver receives balance's observations (core/record). A nil field
// observes nothing; observeBalance fills every field or none.
type balanceObserver struct {
	Quota func(record.QuotaObservation)
	Route func(record.RouteObservation)
	Reset func(record.ResetObservation)
}

// resetSlack is how far apart two reads of one window's reset may be and still
// be the same reset: reset_in_min rounds up to the minute, and a vendor may
// move a reset by seconds between reads.
const resetSlack = 2 * 60

// openAccountTelemetry opens the store for account events: quota, route and
// reset. A project that turned its own partition off still writes them; the
// user switch and DUOLOG_TELEMETRY=off stop them. It returns nil when they are
// off or the store cannot be used.
func openAccountTelemetry(cfg *config.Config, root string) *telemetry.Store {
	if !cfg.Telemetry.Enabled {
		return nil
	}
	o := telemetryOptions(cfg, root)
	o.ProjectEnabled = false
	s, err := telemetry.Open(o)
	if err != nil {
		return nil
	}
	return s
}

// observeBalance mirrors balance's observations into s as account events. It
// only records what balance already read or decided: it never reads a vendor,
// and balance never reads what it writes. An append that fails is counted in
// the store's health and otherwise ignored. A nil store observes nothing.
func observeBalance(s *telemetry.Store) balanceObserver {
	if s == nil {
		return balanceObserver{}
	}
	m := &quotaMirror{s: s, now: time.Now, seen: map[windowKey][]windowValue{}}
	return balanceObserver{
		Quota: m.observe,
		Route: func(r record.RouteObservation) { _ = s.Append(routeEvent(r)) },
		Reset: func(r record.ResetObservation) { _ = s.Append(telemetry.Reset{Agent: r.Agent, Reason: r.Reason}) },
	}
}

// windowKey is one window of one agent and epoch; windowValue is what a read
// of it found, with reset as the instant reset_in_min gives, 0 for none.
type windowKey struct {
	agent        string
	epoch        int64
	kind, family string
	lenMin       int64
}

type windowValue struct {
	usedBP int64
	status string
	reset  int64
}

func (v windowValue) same(w windowValue) bool {
	if v.usedBP != w.usedBP || v.status != w.status || (v.reset == 0) != (w.reset == 0) {
		return false
	}
	return v.reset == 0 || (v.reset-w.reset <= resetSlack && w.reset-v.reset <= resetSlack)
}

// quotaMirror writes the windows of a snapshot that changed since the last
// one written for the same agent, epoch, window, family and reset. It starts
// from the quota events the store holds, so a new process writes no snapshot
// an earlier one already wrote.
type quotaMirror struct {
	s   *telemetry.Store
	now func() time.Time

	mu     sync.Mutex
	seeded bool
	seen   map[windowKey][]windowValue
}

func (m *quotaMirror) observe(q record.QuotaObservation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.seeded {
		m.seed()
	}
	now := m.now()
	current := map[windowKey][]windowValue{}
	var changed []telemetry.Window
	for _, o := range q.Windows {
		w, ok := quotaWindow(o, now)
		if !ok {
			continue
		}
		k := windowKey{agent: q.Agent, epoch: q.Epoch, kind: w.Kind, family: w.Family, lenMin: w.LenMin}
		v := windowValue{usedBP: w.UsedBP, status: w.Status, reset: resetAt(now.Unix(), w.ResetInMin)}
		if !slices.ContainsFunc(m.seen[k], v.same) {
			changed = append(changed, w)
		}
		current[k] = append(current[k], v)
	}
	if len(changed) == 0 {
		return
	}
	for _, e := range quotaEvents(q, changed, now) {
		if m.s.Append(e) != nil {
			// Not remembered: the next read tries these windows again.
			return
		}
	}
	maps.Copy(m.seen, current)
}

// seed remembers the windows of the quota events the store holds, each
// window as the latest event that carried it left it. A store it cannot read
// leaves nothing to remember, and every window is written again.
func (m *quotaMirror) seed() {
	m.seeded = true
	events, err := m.s.Read(telemetry.Query{})
	if err != nil {
		return
	}
	for _, e := range events {
		q, ok := e.(telemetry.Quota)
		if !ok {
			continue
		}
		event := map[windowKey][]windowValue{}
		for _, w := range q.Windows {
			k := windowKey{agent: q.Agent, epoch: q.Epoch, kind: w.Kind, family: w.Family, lenMin: w.LenMin}
			event[k] = append(event[k], windowValue{usedBP: w.UsedBP, status: w.Status, reset: resetAt(q.T, w.ResetInMin)})
		}
		maps.Copy(m.seen, event)
	}
}

// quotaWindow maps an observed window to its record with every number in
// range and the reset counted in minutes from now, rounded up. A window with
// no length the record can hold is left out.
func quotaWindow(o record.WindowObservation, now time.Time) (telemetry.Window, bool) {
	if o.LengthMinutes < 1 || o.LengthMinutes > maxSecs {
		return telemetry.Window{}, false
	}
	w := telemetry.Window{Kind: o.Kind, Family: o.Family, Status: o.Status, LenMin: o.LengthMinutes, UsedBP: min(max(o.UsedBasisPoints, 0), 10_000)}
	if !o.ResetAt.IsZero() && o.ResetAt.After(now) {
		w.ResetInMin = min(int64((o.ResetAt.Sub(now)+time.Minute-1)/time.Minute), maxSecs)
	}
	return w, true
}

// resetAt is the instant a reset_in_min counted from t names, 0 for none.
func resetAt(t, resetInMin int64) int64 {
	if resetInMin <= 0 {
		return 0
	}
	return t + resetInMin*60
}

// quotaEvents puts windows into one quota event when its line fits in 1 KiB.
// A larger snapshot is split into events that each hold windows of one kind,
// as many as fit, so every window is written once.
func quotaEvents(q record.QuotaObservation, windows []telemetry.Window, now time.Time) []telemetry.Quota {
	base := telemetry.Quota{Agent: q.Agent, Src: q.Source, Plan: q.Plan, Epoch: q.Epoch, Overage: q.Overage, SpendControl: q.SpendControl}
	fits := func(e telemetry.Quota) bool {
		_, err := telemetry.Encode(e, telemetry.Names{}, now)
		return err == nil
	}
	whole := base
	whole.Windows = windows
	if fits(whole) {
		return []telemetry.Quota{whole}
	}
	var kinds []string
	groups := map[string][]telemetry.Window{}
	for _, w := range windows {
		if _, ok := groups[w.Kind]; !ok {
			kinds = append(kinds, w.Kind)
		}
		groups[w.Kind] = append(groups[w.Kind], w)
	}
	var out []telemetry.Quota
	for _, kind := range kinds {
		e := base
		for _, w := range groups[kind] {
			next := e
			next.Windows = append(slices.Clone(e.Windows), w)
			if len(e.Windows) > 0 && !fits(next) {
				out = append(out, e)
				next = base
				next.Windows = []telemetry.Window{w}
			}
			e = next
		}
		out = append(out, e)
	}
	return out
}

func routeEvent(r record.RouteObservation) telemetry.Route {
	target := func(t record.RouteTarget) telemetry.Target {
		return telemetry.Target{Agent: t.Agent, Family: t.Family, Effort: t.Effort}
	}
	e := telemetry.Route{Role: r.Role, Class: r.Class, Chosen: target(r.Chosen), Incumbent: target(r.Incumbent), Rule: r.Rule, ScoreMin: r.ScoreMin}
	e.Candidates = make([]telemetry.Target, len(r.Candidates))
	for i, t := range r.Candidates {
		e.Candidates[i] = target(t)
	}
	return e
}

// quotaObservation maps a snapshot duolog quota read to what balance would
// observe: windows by kind and family, usage in basis points, no plan name
// outside the known ones, and no credit balance, identity or vendor id.
func quotaObservation(agent string, s quota.Snapshot) record.QuotaObservation {
	q := record.QuotaObservation{Agent: agent, Source: "app_server", Plan: s.Plan, SpendControl: s.SpendControlReached}
	if agent == "claude" {
		q.Source = "usage_read"
	}
	q.Overage = (s.ExtraUsage != nil && s.ExtraUsage.Enabled) || (s.Credits != nil && (s.Credits.HasCredits || s.Credits.Unlimited))
	for _, w := range s.Windows {
		q.Windows = append(q.Windows, windowObservation(agent, "", w))
	}
	families := make([]string, 0, len(s.ModelLimits))
	for family := range s.ModelLimits {
		families = append(families, family)
	}
	sort.Strings(families)
	for _, family := range families {
		for _, w := range s.ModelLimits[family] {
			q.Windows = append(q.Windows, windowObservation(agent, family, w))
		}
	}
	return q
}

// windowObservation names a window by its length (five_hour, weekly or other)
// and the family it limits: all for a window of every model, the limit's
// family, or the family of its kind or model.
func windowObservation(agent, family string, w quota.Window) record.WindowObservation {
	o := record.WindowObservation{Kind: "other", Family: family, Status: "unknown", LengthMinutes: int64(w.DurationMinutes), ResetAt: w.ResetsAt}
	switch w.DurationMinutes {
	case 300:
		o.Kind = "five_hour"
	case 10080:
		o.Kind = "weekly"
	}
	if o.Family == "" {
		kindFamily, scoped := strings.CutPrefix(w.Kind, "weekly_")
		switch {
		case w.Model != "":
			o.Family = coreroute.Normalize(agent, w.Model)
		case scoped && kindFamily != "all" && kindFamily != "scoped":
			o.Family = kindFamily
		case w.Kind == "other" || w.Kind == "weekly_scoped":
			o.Family = "other"
		default:
			o.Family = "all"
		}
	}
	// The comparisons also refuse NaN.
	if p := math.Round(w.UsedPercent * 100); p >= 0 {
		o.UsedBasisPoints = int64(min(p, 10_000))
	}
	switch w.Severity {
	case "rejected", "blocked", "exceeded":
		o.Status = "blocked"
	case "", "normal", "warning", "allowed", "allowed_warning":
		o.Status = "ok"
	}
	if w.UsedPercent >= 100 {
		o.Status = "blocked"
	}
	return o
}

// routeObservation maps a route the router resolved: the role's target is the
// incumbent, the route's own target the chosen one, and the rule says why
// they differ, if they do.
func routeObservation(cfg *config.Config, role string, route router.Route, opts router.Options) record.RouteObservation {
	use := cfg.Roles[role].Use
	rule := "pinned"
	if opts.Use != nil {
		use, rule = *opts.Use, "explicit"
	}
	if route.IsFallback {
		rule = "availability"
	}
	target := func(t config.Target) record.RouteTarget {
		return record.RouteTarget{Agent: t.Agent, Family: coreroute.Normalize(t.Agent, t.Model), Effort: t.Effort}
	}
	o := record.RouteObservation{Role: role, Incumbent: target(use), Chosen: target(route.Target), Rule: rule}
	o.Candidates = []record.RouteTarget{o.Incumbent}
	if o.Chosen != o.Incumbent {
		o.Candidates = append(o.Candidates, o.Chosen)
	}
	return o
}
