package calibration

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// paidBudget is an agent on a paid route with both opt-ins and cap USD to
// spend, of which spent and inflight are gone.
func paidBudget(cap, spent, inflight float64) BillingBudget {
	return BillingBudget{Verdict: VerdictPaid, Account: "personal", UsableShare: 1, CapUSD: cap, SpentUSD: spent,
		InflightUSD: inflight, Allow: true, Calibrate: true}
}

// agentUSD is the plan's high USD of agent: its cells, and its judge calls
// and rewrites.
func agentUSD(p Plan, agent string) (cells, judges float64) {
	for _, c := range p.Classes {
		cells += c.AgentUSD[agent]
		judges += c.JudgeUSD[agent]
	}
	return cells, judges
}

// A calibration step on a paid route needs paid_runs.<agent>.allow and
// calibrate. With both, the plan shows the agent's high USD estimate, what it
// spent and has in flight, its cap, the price caveat and the cuts. Judge
// calls and rewrites are costed before cells: the cells are cut until the
// agent's whole cost fits what the cap leaves, and that cost is never left
// out. The billing is in the hash, so a changed route, account, share or
// approved USD needs a new approval; on resume the stricter of the approved
// and the current budget holds.
func TestCalibrationPlanPaidOptInCutAndApproval(t *testing.T) {
	cfg := testConfig(t)
	m := planManifest("tests")
	plan := func(b BillingBudget) (Plan, error) {
		return BuildPlan(PlanInput{Cfg: cfg, Manifest: m, Rates: smallRates(), RunID: "cal-test",
			Billing: map[string]BillingBudget{"claude": b}})
	}

	for _, b := range []BillingBudget{
		{Verdict: VerdictPaid, Account: "personal", UsableShare: 1, CapUSD: 100},
		{Verdict: VerdictPaid, Account: "personal", UsableShare: 1, CapUSD: 100, Allow: true},
	} {
		if _, err := plan(b); err == nil || !strings.Contains(err.Error(), "paid_runs.claude.calibrate") {
			t.Fatalf("a paid route without both opt-ins: %v", err)
		}
	}

	full, err := plan(paidBudget(1000, 1.5, 0.5))
	if err != nil {
		t.Fatal(err)
	}
	cells, judges := agentUSD(full, "claude")
	approved := full.Billing["claude"].ApprovedUSD
	if cells <= 0 || judges <= 0 || !near(approved, cells+judges) || len(full.Cut) != 0 {
		t.Fatalf("full plan: cells %v judges %v approved %v cut %v", cells, judges, approved, full.Cut)
	}
	text := render(t, full)
	for _, want := range []string{"paid claude:", "USD high estimate", "spent 1.50 and 0.50 in flight of the 1000 USD cap",
		"prices are a lower bound"} {
		if !strings.Contains(text, want) {
			t.Errorf("plan lacks %q:\n%s", want, text)
		}
	}

	// Room for the judge calls and a part of the cells only.
	room := judges + cells/3
	tight, err := plan(paidBudget(room+2, 1.5, 0.5))
	if err != nil {
		t.Fatal(err)
	}
	cut, kept := agentUSD(tight, "claude")
	approved = tight.Billing["claude"].ApprovedUSD
	if len(tight.Cut) == 0 || approved > room+1e-9 || !near(approved, cut+kept) || kept <= 0 {
		t.Fatalf("tight plan: approved %v of room %v, cells %v judges %v, cut %v", approved, room, cut, kept, tight.Cut)
	}
	if !strings.Contains(render(t, tight), "cut to fit the cap:") {
		t.Fatal("the cuts are not shown")
	}
	// Not even all judge calls fit: tasks go, with their judge calls, and the
	// judge cost of what stays is still counted.
	small, err := plan(paidBudget(judges/2+2, 1.5, 0.5))
	if err != nil {
		t.Fatal(err)
	}
	c, j := agentUSD(small, "claude")
	if got := small.Billing["claude"].ApprovedUSD; got > judges/2+1e-9 || !near(got, c+j) {
		t.Fatalf("small cap: approved %v, cells %v, judges %v", got, c, j)
	}

	// The billing is in the hash.
	base := full.Hash
	for _, change := range []func(*BillingBudget){
		func(b *BillingBudget) { b.Verdict = VerdictOverage },
		func(b *BillingBudget) { b.Account, b.UsableShare = "workspace", 0.5 },
		func(b *BillingBudget) { b.ApprovedUSD += 1 },
	} {
		p := full
		b := p.Billing["claude"]
		change(&b)
		p.Billing = map[string]BillingBudget{"claude": b}
		if p.ComputeHash() == base {
			t.Fatalf("hash ignores the change to %+v", b)
		}
	}
	run := &Run{Seed: 1}
	if err := Approve(run, full, time.Now()); err != nil {
		t.Fatal(err)
	}
	if run.Approved.Hash != full.Hash || tight.Hash == full.Hash {
		t.Fatal("a plan with another budget kept the approval")
	}

	// On resume the stricter budget holds: a lower cap, a withdrawn opt-in,
	// a smaller usable share, a route that turned paid.
	current := map[string]BillingBudget{"claude": {Verdict: VerdictPaid, Account: "workspace", UsableShare: 0.5, CapUSD: 5, SpentUSD: 4,
		Allow: true, Calibrate: false}}
	limited := full.LimitBilling(current)["claude"]
	if limited.CapUSD != 5 || limited.SpentUSD != 4 || limited.Calibrate || limited.UsableShare != 0.5 || limited.Account != "workspace" {
		t.Fatalf("limited budget: %+v", limited)
	}
	if d := AdmitBilled("claude", limited, nil, CellCost{WeeklyPct: 1}, Spend{}, testCalibration(), 0, testTime); d.Admit {
		t.Fatal("a withdrawn calibrate opt-in still admits")
	}
	sub, err := BuildPlan(PlanInput{Cfg: cfg, Manifest: m, Rates: smallRates(), RunID: "cal-test",
		Billing: map[string]BillingBudget{"claude": {Verdict: VerdictSubscription, Account: "personal", UsableShare: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	turned := sub.LimitBilling(map[string]BillingBudget{"claude": paidBudget(100, 0, 0)})["claude"]
	if d := AdmitBilled("claude", turned, []Window{weekly(10), fiveHour(10)}, CellCost{WeeklyPct: 1, WindowPct: 1}, Spend{}, testCalibration(), 0, testTime); d.Admit {
		t.Fatal("paid work the plan never approved was admitted")
	}
	// An old plan without billing keeps its hash and its admission.
	old := full
	old.Billing = nil
	for i := range old.Classes {
		old.Classes[i].AgentUSD, old.Classes[i].JudgeUSD = nil, nil
	}
	old.Hash = old.ComputeHash()
	if strings.Contains(string(mustJSON(t, old)), "Billing") || strings.Contains(string(mustJSON(t, old)), "AgentUSD") {
		t.Fatal("an old plan's JSON gained billing fields")
	}
	// It runs on as it did, but paid work needs a new approval first.
	if d := AdmitBilled("claude", old.LimitBilling(nil)["claude"], []Window{weekly(10), fiveHour(10)}, CellCost{WeeklyPct: 1, WindowPct: 1}, Spend{}, testCalibration(), 0, testTime); !d.Admit {
		t.Fatalf("an old plan on a subscription: %+v", d)
	}
	if d := AdmitBilled("claude", old.LimitBilling(map[string]BillingBudget{"claude": paidBudget(100, 0, 0)})["claude"], nil, CellCost{WeeklyPct: 1}, Spend{}, testCalibration(), 0, testTime); d.Admit {
		t.Fatal("an old plan ran paid work it never approved")
	}
}

// Window math scales by the usable share once, USD never does. A personal
// account is unchanged. A workspace account at u=0.5 has calibration caps of
// 5% weekly and 25% of a five-hour window of the whole window, and keeps the
// lead reserves, 15% weekly and 10% five-hour, of its usable part. Rates stay
// real. A plan on subscription_overage fits below the lead reserve before it
// can be approved. A paid route needs no window and is never stopped for the
// lack of one; a subscription route is.
func TestCalibrationUsableShareNoDoubleScale(t *testing.T) {
	cal := testCalibration()
	personal := BillingBudget{Verdict: VerdictSubscription, Account: "personal", UsableShare: 1}
	workspace := BillingBudget{Verdict: VerdictSubscription, Account: "workspace", UsableShare: 0.5}
	cost := CellCost{WeeklyPct: 1, WindowPct: 1}
	ws := func(weeklyUsed, fiveUsed float64) []Window { return []Window{weekly(weeklyUsed), fiveHour(fiveUsed)} }

	cases := []struct {
		name      string
		ws        []Window
		spend     Spend
		personal  Decision
		workspace Decision
	}{
		{"weekly cap", ws(10, 10), Spend{WeeklyPct: map[string]float64{"claude": 4.5}}, Decision{Admit: true}, Decision{Stop: "approved cap"}},
		{"weekly reserve", ws(41, 10), Spend{}, Decision{Admit: true}, Decision{Admit: true}},
		{"weekly reserve passed", ws(42, 10), Spend{}, Decision{Admit: true}, Decision{Stop: "lead reserve"}},
		{"five-hour reserve", ws(10, 44), Spend{}, Decision{Admit: true}, Decision{Admit: true}},
		{"five-hour reserve passed", ws(10, 45), Spend{}, Decision{Admit: true}, Decision{Stop: "lead reserve"}},
	}
	for _, c := range cases {
		if got := AdmitBilled("claude", personal, c.ws, cost, c.spend, cal, 0, testTime); got != c.personal {
			t.Errorf("%s personal: %+v, want %+v", c.name, got, c.personal)
		}
		if got, old := AdmitBilled("claude", personal, c.ws, cost, c.spend, cal, 0, testTime), Admit("claude", c.ws, cost, c.spend, cal, 0, testTime); got != old {
			t.Errorf("%s: a personal account changed admission: %+v, was %+v", c.name, got, old)
		}
		if got := AdmitBilled("claude", workspace, c.ws, cost, c.spend, cal, 0, testTime); got != c.workspace {
			t.Errorf("%s workspace: %+v, want %+v", c.name, got, c.workspace)
		}
	}
	// The five-hour calibration cap: 25% of the whole window at u=0.5.
	own := Spend{Windows: map[string]WindowSpend{"claude": {ResetsAt: testTime.Add(time.Hour), StartUsed: 0, LastUsed: 24.5}}}
	if d := AdmitBilled("claude", workspace, ws(10, 30), cost, own, cal, 0, testTime); d.Wait.IsZero() {
		t.Fatalf("workspace five-hour cap: %+v", d)
	}
	if d := AdmitBilled("claude", personal, ws(10, 30), cost, own, cal, 0, testTime); !d.Admit {
		t.Fatalf("personal five-hour cap: %+v", d)
	}
	// Rates stay real: a cell costs the same points on either account.
	rate := Rate{WeeklyPerMTok: 2, WindowPerMTok: 4}
	if c := CostOf(&rate, 500000, nil); c.WeeklyPct != 1 || c.WindowPct != 2 {
		t.Fatalf("rate scaled: %+v", c)
	}

	// A plan on a workspace account fits the usable cap, its cells costed in
	// real points.
	cfg := testConfig(t)
	m := planManifest("tests")
	rates := map[string]Rate{"codex": {WeeklyPerMTok: 0.8, WindowPerMTok: 1}, "claude": {WeeklyPerMTok: 0.1, WindowPerMTok: 0.1}}
	build := func(codex BillingBudget, quota map[string][]Window) Plan {
		return buildPlan(t, PlanInput{Cfg: cfg, Manifest: m, Rates: rates, Quota: quota,
			Billing: map[string]BillingBudget{"codex": codex}})
	}
	weeklyOf := func(p Plan, agent string) float64 {
		var sum float64
		for _, c := range p.Classes {
			sum += c.Share[agent].WeeklyHigh
		}
		return sum
	}
	whole := build(personal, nil)
	if len(whole.Cut) != 0 || weeklyOf(whole, "codex") <= 5 {
		t.Fatalf("personal plan: codex %v%%, cut %v", weeklyOf(whole, "codex"), whole.Cut)
	}
	half := build(workspace, nil)
	if len(half.Cut) == 0 || weeklyOf(half, "codex") > 5+1e-9 {
		t.Fatalf("workspace plan: codex %v%%, cut %v", weeklyOf(half, "codex"), half.Cut)
	}
	if !strings.Contains(render(t, half), "usable share 0.5") {
		t.Fatal("the plan does not name the usable share")
	}

	// subscription_overage: the plan fits below the lead reserve.
	high := map[string][]Window{"codex": ws(80, 10)}
	if p := build(personal, high); len(p.Cut) != 0 {
		t.Fatalf("a subscription plan is cut by the reserve before approval: %v", p.Cut)
	}
	overage := BillingBudget{Verdict: VerdictOverage, Account: "personal", UsableShare: 1}
	p := build(overage, high)
	if len(p.Cut) == 0 || 80+weeklyOf(p, "codex")+ReserveWeekly > 100+1e-9 {
		t.Fatalf("overage plan above the reserve: codex %v%%, cut %v", weeklyOf(p, "codex"), p.Cut)
	}

	// A paid route: no window needed, USD bounds the plan.
	paid := paidBudget(1000, 0, 0)
	if d := AdmitBilled("codex", paid, nil, cost, Spend{}, cal, 0, testTime); !d.Admit {
		t.Fatalf("a paid route without windows: %+v", d)
	}
	if d := AdmitBilled("codex", personal, nil, cost, Spend{}, cal, 0, testTime); d.Stop != "quota unavailable" {
		t.Fatalf("a subscription route without windows: %+v", d)
	}
	codexRatesPaid := buildPlan(t, PlanInput{Cfg: cfg, Manifest: m, Rates: codexRates(), Billing: map[string]BillingBudget{"codex": paid}})
	if len(codexRatesPaid.Cut) != 0 || codexRatesPaid.Billing["codex"].ApprovedUSD <= 0 {
		t.Fatalf("a paid route was cut by window shares: %v", codexRatesPaid.Cut)
	}
}
