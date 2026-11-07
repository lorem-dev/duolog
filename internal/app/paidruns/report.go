package paidruns

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// Report is how one agent's launch shape is billed, for quota and doctor:
// the assessment; the agent's rolling 24-hour spend, its paid tasks in
// flight, the cap when paid runs are allowed, and the part of the spend
// counted at estimates because the runs reported no tokens; the agent's
// session tier and live sessions, and those of all agents; the names the
// process withholds, the subscription endpoint entries that matched, the
// fast-mode pin and the config keys Load ignored. It holds names and numbers
// only: no environment, settings, URL or identity.
type Report struct {
	Assessment      Assessment            `json:"assessment"`
	SpentUSD        float64               `json:"spent_usd"`
	InflightUSD     float64               `json:"inflight_usd"`
	CapUSD          float64               `json:"cap_usd"`
	EstimatedUSD    float64               `json:"estimated_usd"`
	Sessions        TierResult            `json:"sessions"`
	TotalLive       int                   `json:"total_live"`
	TotalLimit      int                   `json:"total_limit"`
	Dropped         []string              `json:"dropped,omitempty"`
	EndpointEntries []string              `json:"endpoint_entries,omitempty"`
	FastPinned      bool                  `json:"fast_pinned"`
	Notices         []config.ConfigNotice `json:"notices,omitempty"`
	ReadError       string                `json:"read_error,omitempty"`
	Held            []Held                `json:"held,omitempty"`
}

// Held is a hold of any agent that reconciliation keeps without a
// conclusion. Name is its peer id, else its reservation id: what duolog
// peer release takes. EstimateUSD is the paid estimate it keeps off the cap.
type Held struct {
	Name        string          `json:"name"`
	Agent       string          `json:"agent"`
	Shape       workerenv.Shape `json:"shape"`
	Created     time.Time       `json:"created"`
	EstimateUSD float64         `json:"estimate_usd,omitempty"`
	Reason      string          `json:"reason"`
}

// held lists the retained holds of view, one per name, oldest first.
func (st State) held() []Held {
	var out []Held
	for _, id := range slices.Sorted(maps.Keys(st.Reservations)) {
		r := st.Reservations[id]
		if r.Retained == "" {
			continue
		}
		name := cmp.Or(r.Peer, r.ID)
		i := slices.IndexFunc(out, func(h Held) bool { return h.Name == name })
		if i < 0 {
			out = append(out, Held{Name: name, Agent: r.Agent, Shape: r.Shape, Created: r.Created, Reason: r.Retained})
			i = len(out) - 1
		}
		if r.Paid && r.Task {
			out[i].EstimateUSD += r.Estimate.USD
		}
		if r.Created.Before(out[i].Created) {
			out[i].Created = r.Created
		}
	}
	slices.SortStableFunc(out, func(a, b Held) int { return a.Created.Compare(b.Created) })
	return out
}

// Report explains the billing of the launch in describes, without admitting
// or running anything and without writing the account state.
//
// A worker or a peer is built, and read, as a launch of that shape would be,
// through the cache. The lead is built from its observed command and
// environment, kept byte for byte, and never run: its route signals are
// judged against the agent's latest free read in the cache. Without one the
// lead's verdict is paid, never the worker's.
//
// A failed read keeps the route evidence it found, the verdict is paid, and
// the report comes back with the read's error. Run evidence that settled
// after the read, such as a run on paid overage, counts as for admission.
func (g *Gate) Report(ctx context.Context, in workerenv.ProcessInput) (Report, error) {
	return g.report(ctx, in, false)
}

// assess builds the process that in describes and judges it, as Report does:
// a worker or a peer against a read through the cache, or with cached against
// the agent's latest read there, and a lead against that latest read. readErr
// is the read's failure, which still gives an assessment; err gives none.
func (g *Gate) assess(ctx context.Context, in workerenv.ProcessInput, cached bool) (p workerenv.Process, s quota.Snapshot, a Assessment, readErr, err error) {
	agent := in.Agent
	switch in.Shape {
	case workerenv.Worker, workerenv.Peer:
		if !cached {
			p, s, a, readErr = g.Cache.Get(ctx, g.build(ctx, Request{Input: in}), g.Config, quota.RunEvidence{}, false)
			if a.Agent == "" {
				return p, s, a, readErr, readErr
			}
			return p, s, a, readErr, nil
		}
		if p, err = g.build(ctx, Request{Input: in})(); err != nil {
			return p, s, a, nil, err
		}
		var ok bool
		if s, ok = g.Cache.Latest(agent); !ok {
			readErr = fmt.Errorf("no quota read of %s in the cache", agent)
		}
	case workerenv.Lead:
		if g.Layers != nil {
			if in.Layers, err = g.Layers(ctx, in); err != nil {
				return p, s, a, nil, err
			}
		}
		if p, err = workerenv.Build(in); err != nil {
			return p, s, a, nil, err
		}
		var ok bool
		if s, ok = g.Cache.Latest(agent); !ok {
			readErr = fmt.Errorf("no free quota read of %s to judge the lead's route with", agent)
		}
	default:
		return p, s, a, nil, fmt.Errorf("paid runs: unknown shape %q", in.Shape)
	}
	return p, s, Evaluate(p, s, readErr, quota.RunEvidence{}, g.Config, g.now()), readErr, nil
}

// ReportCached is Report without a quota read: a worker or a peer is judged
// against the agent's latest read in the cache, as a lead is, and the
// verdict is paid when there is none. Without one, its sessions are those of
// the tier admission recorded last, not the small tier a paid verdict
// forces: nothing was read that could have changed it. The lead's hooks use
// it, so that a prompt never waits for a read.
func (g *Gate) ReportCached(ctx context.Context, in workerenv.ProcessInput) (Report, error) {
	return g.report(ctx, in, true)
}

func (g *Gate) report(ctx context.Context, in workerenv.ProcessInput, cached bool) (Report, error) {
	if g.Store == nil || g.Cache == nil || g.Config == nil {
		return Report{}, errors.New("paid runs: the gate needs a store, a cache and a config")
	}
	in.Config = g.Config
	agent := in.Agent
	p, s, a, readErr, err := g.assess(ctx, in, cached)
	if err != nil {
		return Report{}, err
	}

	var view State
	if err := g.Store.View(ctx, func(st State) error { view = st; return nil }); err != nil {
		return Report{}, errors.Join(readErr, err)
	}
	if e, ok := view.runEvidence(agent, s.ReadAt); ok {
		a = Evaluate(p, s, readErr, e, g.Config, g.now())
	}
	now := g.now()
	r := Report{Assessment: a, Dropped: p.Dropped, EndpointEntries: p.EndpointEntries, FastPinned: p.FastPinned,
		Notices: g.Config.Notices, TotalLimit: g.totalLimit(), Held: view.held()}
	r.SpentUSD, r.InflightUSD = view.spent(agent, now)
	for _, settled := range view.Settled {
		if settled.Agent == agent && settled.Started && !settled.CompleteUsage && now.Sub(settled.Ended) < capSpan {
			r.EstimatedUSD += settled.ChargedUSD
		}
	}
	if paid := g.Config.PaidRuns.Agent(agent); paid.Allow {
		r.CapUSD = paid.DailyCapUSD
	}
	r.Sessions = g.reportTier(view, agent, a, s, cached && in.Shape != workerenv.Lead)
	r.Sessions.Live, r.TotalLive = view.sessions(agent)
	if readErr != nil {
		r.ReadError = readErr.Error()
	}
	return r, readErr
}

// reportTier is the session tier a report shows for assessment a on read s:
// the tier s and the agent's rate samples give, or, for a cached report of a
// worker or a peer with no read in the cache, the tier admission recorded
// last.
func (g *Gate) reportTier(view State, agent string, a Assessment, s quota.Snapshot, cached bool) TierResult {
	if cached && s.ReadAt.IsZero() {
		return lastTier(view.Tiers[agent], g.Config)
	}
	fingerprint := Fingerprint(agent, view.AccountKeys[agent+"/"+quota.IdentityAccount], s)
	samples := slices.DeleteFunc(slices.Clone(view.Samples), func(x RateSample) bool {
		return x.Agent != agent || x.Fingerprint != fingerprint
	})
	return SelectTier(a, s, samples, view.Tiers[agent], g.Config)
}

// lastTier is the tier admission recorded last for an agent, with the
// session limit cfg gives that tier now. With none recorded, it is the small
// tier every agent starts at.
func lastTier(last TierResult, cfg *config.Config) TierResult {
	tiers, _ := tierSettings(cfg)
	if !slices.Contains(tierOrder, last.Tier) {
		return TierResult{Tier: TierSmall, Reason: "no tier recorded yet", Limit: tiers.Limit(TierSmall)}
	}
	reason := "admission's last tier"
	if last.Reason != "" {
		reason += ": " + last.Reason
	}
	return TierResult{Tier: last.Tier, Reason: reason, Rates: last.Rates, Limit: tiers.Limit(last.Tier)}
}

// SessionWindow is the use of the agent's five-hour window, in percent, as
// the latest admission the account state keeps measured it, and whether it
// is known: an admission no older than maxAge whose window has not reset
// since. It reads no quota.
func (g *Gate) SessionWindow(ctx context.Context, agent string, maxAge time.Duration) (float64, bool, error) {
	if g.Store == nil {
		return 0, false, errors.New("paid runs: the gate needs a store")
	}
	var view State
	if err := g.Store.View(ctx, func(st State) error { view = st; return nil }); err != nil {
		return 0, false, err
	}
	now := g.now()
	var at time.Time
	used, known := 0.0, false
	for _, r := range view.Reservations {
		if r.Agent != agent || now.Sub(r.Created) > maxAge || r.Created.After(now) || !r.Created.After(at) {
			continue
		}
		for _, w := range r.Estimate.Windows {
			if w.Key.Kind == "session" && w.Key.Family == "" && w.Key.Reset.After(now) {
				// Used is the read's percent over 100: round off the float error.
				at, used, known = r.Created, math.Round(w.Used*1e4)/1e2, true
			}
		}
	}
	return used, known, nil
}

// Read reads the free quota of the worker or peer that in describes through
// the route-aware cache: afresh when fresh is set, else a read no older than
// the quota age limit. It returns the read and its assessment, the paid one
// of a failed read with its error, and admits nothing.
func (g *Gate) Read(ctx context.Context, in workerenv.ProcessInput, fresh bool) (quota.Snapshot, Assessment, error) {
	if g.Cache == nil || g.Config == nil {
		return quota.Snapshot{}, Assessment{}, errors.New("paid runs: the gate needs a cache and a config")
	}
	if in.Shape != workerenv.Worker && in.Shape != workerenv.Peer {
		return quota.Snapshot{}, Assessment{}, fmt.Errorf("paid runs: a %s is never read", in.Shape)
	}
	_, s, a, err := g.Cache.Get(ctx, g.build(ctx, Request{Input: in}), g.Config, quota.RunEvidence{}, fresh)
	return s, a, err
}

// Lines says the report in short lines. The worker's lines also carry what
// belongs to the agent: its spend and its sessions. What belongs to the
// account and the config is in AccountLines.
func (r Report) Lines() []string {
	a := r.Assessment
	who := a.Agent + " " + string(a.Shape)
	head := who + ": " + string(a.Verdict)
	if a.Verdict == Paid {
		head += ": " + signalList(a.Signals)
		// duolog never launches the lead, so it spends nothing for it.
		if r.CapUSD <= 0 && a.Shape != workerenv.Lead {
			head += "; duolog will not spend without paid_runs." + a.Agent
		}
	}
	lines := []string{head}
	has := func(name string) bool {
		return slices.ContainsFunc(a.Signals, func(s workerenv.Signal) bool { return s.Name == name })
	}
	if a.Verdict == SubscriptionOverage && has("overage:enabled") {
		lines = append(lines, "paid overage is on; duolog stops before it")
	}
	if has("run:overage") {
		lines = append(lines, who+" ran on paid overage; duolog stopped further runs")
	}
	if a.Account == "workspace" {
		lines = append(lines, fmt.Sprintf("account workspace, usable share %g (%s)", a.UsableShare, strings.Join(a.AccountReasons, ", ")))
	} else if a.Account != "" {
		lines = append(lines, "account personal")
	}
	if len(r.EndpointEntries) > 0 {
		lines = append(lines, "subscription endpoint: "+strings.Join(r.EndpointEntries, ", "))
	}
	if r.FastPinned {
		lines = append(lines, "fast mode pinned off")
	}
	if len(r.Dropped) > 0 {
		lines = append(lines, "withheld: "+strings.Join(r.Dropped, ", "))
	}
	if a.Shape != workerenv.Worker {
		return lines
	}
	switch {
	case r.CapUSD > 0:
		lines = append(lines, fmt.Sprintf("paid allowed: %.2f of %g USD in 24 h, %.2f in flight", r.SpentUSD, r.CapUSD, r.InflightUSD))
	case r.SpentUSD > 0 || r.InflightUSD > 0:
		lines = append(lines, fmt.Sprintf("spent %.2f USD in 24 h, %.2f in flight", r.SpentUSD, r.InflightUSD))
	}
	if r.EstimatedUSD > 0 {
		lines = append(lines, fmt.Sprintf("%.2f of it at estimates: peers report no token counts", r.EstimatedUSD))
	}
	t := r.Sessions
	return append(lines, fmt.Sprintf("%s sessions %d of %d (tier %s: %s)", a.Agent, t.Live, t.Limit, t.Tier, t.Reason))
}

// AccountLines are what no single agent owns: the live sessions of all
// agents, the holds kept without a conclusion with the command that ends
// each, and the config keys Load ignored. Every report of one gate holds
// the same, so a caller prints them once, from any report, after the
// agents' lines.
func (r Report) AccountLines() []string {
	lines := []string{fmt.Sprintf("sessions %d of %d in all", r.TotalLive, r.TotalLimit)}
	for _, h := range r.Held {
		what := fmt.Sprintf("%s %s, since %s", h.Agent, h.Shape, h.Created.UTC().Format(time.RFC3339))
		if h.EstimateUSD > 0 {
			what += fmt.Sprintf(", %.2f USD", h.EstimateUSD)
		}
		lines = append(lines, fmt.Sprintf("held: %s (%s): %s; end it with duolog peer release %s", h.Name, what, h.Reason, h.Name))
	}
	for _, n := range r.Notices {
		lines = append(lines, "ignored config: "+n.Key+" ("+n.Reason+")")
	}
	return lines
}

// CompareLead says how the lead and the worker of an agent differ, as
// "claude lead: subscription; claude worker: paid (apiKeyHelper)", or ""
// when their verdicts agree.
func CompareLead(lead, worker Report) string {
	if lead.Assessment.Verdict == worker.Assessment.Verdict {
		return ""
	}
	part := func(r Report) string {
		a := r.Assessment
		text := a.Agent + " " + string(a.Shape) + ": " + string(a.Verdict)
		var names []string
		for _, s := range a.Signals {
			if !slices.Contains(names, s.Name) {
				names = append(names, s.Name)
			}
		}
		if len(names) > 0 {
			text += " (" + strings.Join(names, ", ") + ")"
		}
		return text
	}
	return part(lead) + "; " + part(worker)
}

// signalList names each signal with the layer it came from.
func signalList(signals []workerenv.Signal) string {
	parts := make([]string, 0, len(signals))
	for _, s := range signals {
		parts = append(parts, s.Name+" ("+s.Layer+")")
	}
	return strings.Join(parts, ", ")
}

// ReadLayers reads the settings layers of the launch in describes, each time
// it is called, so a build never uses facts older than its admission or its
// report. A lead's own argv layers, such as --settings, come before managed
// policy, which wins as it does in the agent.
//
// A launch without its home folder or platform, or a worker or peer without
// the project root it runs in, has layers duolog cannot find. They count as
// one unreadable layer, a paid signal, so the free variant is refused rather
// than run with its settings unread.
func ReadLayers(_ context.Context, in workerenv.ProcessInput) ([]workerenv.LayerFacts, error) {
	loc := in.Locations
	if loc.Home == "" || loc.GOOS == "" || loc.Root == "" && in.Shape != workerenv.Lead {
		return []workerenv.LayerFacts{{Layer: "locations", Unreadable: true,
			Signals: []workerenv.Signal{{Name: "unreadable", Layer: "locations"}}}}, nil
	}
	var endpoints map[string]string
	if in.Agent == "claude" && in.Config != nil {
		endpoints = in.Config.PaidRuns.Claude.SubscriptionEndpoints
	}
	layers := workerenv.SettingsLayers(in.Agent, in.Locations)
	if in.Shape == workerenv.Lead && len(in.Prefix) > 1 {
		argv := workerenv.ArgvLayers(in.Agent, in.Prefix[1:], in.Locations)
		at := slices.IndexFunc(layers, func(l workerenv.Layer) bool { return l.Managed })
		if at < 0 {
			at = len(layers)
		}
		layers = slices.Insert(layers, at, argv...)
	}
	facts := make([]workerenv.LayerFacts, 0, len(layers))
	for _, layer := range layers {
		facts = append(facts, workerenv.ReadLayer(in.Agent, layer, endpoints, os.ReadFile, os.Stat))
	}
	return facts, nil
}
