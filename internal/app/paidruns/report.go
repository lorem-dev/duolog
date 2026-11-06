package paidruns

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
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
	Assessment      paidrules.Assessment  `json:"assessment"`
	SpentUSD        float64               `json:"spent_usd"`
	InflightUSD     float64               `json:"inflight_usd"`
	CapUSD          float64               `json:"cap_usd"`
	EstimatedUSD    float64               `json:"estimated_usd"`
	Sessions        paidrules.TierResult  `json:"sessions"`
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
	Name        string       `json:"name"`
	Agent       string       `json:"agent"`
	Shape       launch.Shape `json:"shape"`
	Created     time.Time    `json:"created"`
	EstimateUSD float64      `json:"estimate_usd,omitempty"`
	Reason      string       `json:"reason"`
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
func (g *Gate) Report(ctx context.Context, in launch.ProcessInput) (Report, error) {
	if g.Store == nil || g.Cache == nil || g.Config == nil {
		return Report{}, errors.New("paid runs: the gate needs a store, a cache and a config")
	}
	in.Config = g.Config
	agent := in.Agent
	var (
		p       launch.Process
		s       account.Snapshot
		a       paidrules.Assessment
		readErr error
	)
	switch in.Shape {
	case launch.Worker, launch.Peer:
		p, s, a, readErr = g.Cache.Get(ctx, g.build(ctx, Request{Input: in}), g.Config, account.RunEvidence{}, false)
		if a.Agent == "" {
			return Report{}, readErr
		}
	case launch.Lead:
		if g.Layers != nil {
			layers, err := g.Layers(ctx, in)
			if err != nil {
				return Report{}, err
			}
			in.Layers = layers
		}
		var err error
		if p, err = workerenv.Build(in); err != nil {
			return Report{}, err
		}
		var ok bool
		if s, ok = g.Cache.Latest(agent); !ok {
			readErr = fmt.Errorf("no free quota read of %s to judge the lead's route with", agent)
		}
		a = paidrules.Evaluate(p, s, readErr, account.RunEvidence{}, g.Config, g.now())
	default:
		return Report{}, fmt.Errorf("paid runs: unknown shape %q", in.Shape)
	}

	var view State
	if err := g.Store.View(ctx, func(st State) error { view = st; return nil }); err != nil {
		return Report{}, errors.Join(readErr, err)
	}
	if e, ok := view.runEvidence(agent, s.ReadAt); ok {
		a = paidrules.Evaluate(p, s, readErr, e, g.Config, g.now())
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
	fingerprint := paidrules.Fingerprint(agent, view.AccountKeys[agent+"/"+quota.IdentityAccount], s)
	samples := slices.DeleteFunc(slices.Clone(view.Samples), func(x paidrules.RateSample) bool {
		return x.Agent != agent || x.Fingerprint != fingerprint
	})
	r.Sessions = paidrules.SelectTier(a, s, samples, view.Tiers[agent], g.Config)
	r.Sessions.Live, r.TotalLive = view.sessions(agent)
	if readErr != nil {
		r.ReadError = readErr.Error()
	}
	return r, readErr
}

// Read reads the free quota of the worker or peer that in describes through
// the route-aware cache: afresh when fresh is set, else a read no older than
// the quota age limit. It returns the read and its assessment, the paid one
// of a failed read with its error, and admits nothing.
func (g *Gate) Read(ctx context.Context, in launch.ProcessInput, fresh bool) (account.Snapshot, paidrules.Assessment, error) {
	if g.Cache == nil || g.Config == nil {
		return account.Snapshot{}, paidrules.Assessment{}, errors.New("paid runs: the gate needs a cache and a config")
	}
	if in.Shape != launch.Worker && in.Shape != launch.Peer {
		return account.Snapshot{}, paidrules.Assessment{}, fmt.Errorf("paid runs: a %s is never read", in.Shape)
	}
	_, s, a, err := g.Cache.Get(ctx, g.build(ctx, Request{Input: in}), g.Config, account.RunEvidence{}, fresh)
	return s, a, err
}

// Lines says the report in short lines. The worker's lines also carry what
// belongs to the agent: its spend and its sessions. What belongs to the
// account and the config is in AccountLines.
func (r Report) Lines() []string {
	a := r.Assessment
	who := a.Agent + " " + string(a.Shape)
	head := who + ": " + string(a.Verdict)
	if a.Verdict == paidrules.Paid {
		head += ": " + signalList(a.Signals)
		// duolog never launches the lead, so it spends nothing for it.
		if r.CapUSD <= 0 && a.Shape != launch.Lead {
			head += "; duolog will not spend without paid_runs." + a.Agent
		}
	}
	lines := []string{head}
	has := func(name string) bool {
		return slices.ContainsFunc(a.Signals, func(s launch.Signal) bool { return s.Name == name })
	}
	if a.Verdict == paidrules.SubscriptionOverage && has("overage:enabled") {
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
	if a.Shape != launch.Worker {
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
func signalList(signals []launch.Signal) string {
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
func ReadLayers(_ context.Context, in launch.ProcessInput) ([]launch.LayerFacts, error) {
	loc := in.Locations
	if loc.Home == "" || loc.GOOS == "" || loc.Root == "" && in.Shape != launch.Lead {
		return []launch.LayerFacts{{Layer: "locations", Unreadable: true,
			Signals: []launch.Signal{{Name: "unreadable", Layer: "locations"}}}}, nil
	}
	var endpoints map[string]string
	if in.Agent == "claude" && in.Config != nil {
		endpoints = in.Config.PaidRuns.Claude.SubscriptionEndpoints
	}
	layers := workerenv.SettingsLayers(in.Agent, in.Locations)
	if in.Shape == launch.Lead && len(in.Prefix) > 1 {
		argv := workerenv.ArgvLayers(in.Agent, in.Prefix[1:], in.Locations)
		at := slices.IndexFunc(layers, func(l launch.Layer) bool { return l.Managed })
		if at < 0 {
			at = len(layers)
		}
		layers = slices.Insert(layers, at, argv...)
	}
	facts := make([]launch.LayerFacts, 0, len(layers))
	for _, layer := range layers {
		facts = append(facts, workerenv.ReadLayer(in.Agent, layer, endpoints, os.ReadFile, os.Stat))
	}
	return facts, nil
}
