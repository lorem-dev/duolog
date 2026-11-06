package calibration

import (
	"bytes"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
)

// planManifest holds 3 round 1 and 3 round 2 tasks per class. Round 2's
// high estimates differ, so the largest one is known: the fifth task.
func planManifest(classes ...string) Manifest {
	m := Manifest{Counts: map[string]ClassCount{}, Estimates: map[string]Estimate{}, RepoFiles: 500, HistoryDays: 300, CheckSeconds: 30}
	output := map[string]string{"tests": "tests", "docs": "docs"}
	highs := []int64{500000, 500000, 500000, 400000, 600000, 500000}
	var round2 []ManifestTask
	for _, class := range classes {
		out := output[class]
		if out == "" {
			out = "code"
		}
		for i, high := range highs {
			id := fmt.Sprintf("%s-t%d", class, i+1)
			task := ManifestTask{ID: id, Class: class, Statement: fmt.Sprintf("Task %d of %s.\nMore detail.", i+1, class),
				Verification: "hidden_tests", Features: map[string]string{"output": out, "scope": "1-3"}}
			m.Estimates[id] = Estimate{Files: 2, Lines: 40, TokensLow: 100000, TokensHigh: high, Minutes: 10, CheckSeconds: 30}
			if i < 3 {
				m.Tasks = append(m.Tasks, task)
			} else {
				round2 = append(round2, task)
				m.Round2 = append(m.Round2, id)
			}
		}
		m.Counts[class] = ClassCount{Candidates: 9, Valid: 6, Selected: 6}
	}
	m.Tasks = append(m.Tasks, round2...)
	return m
}

func buildPlan(t *testing.T, in PlanInput) Plan {
	t.Helper()
	if in.RunID == "" {
		in.RunID = "cal-test"
	}
	p, err := BuildPlan(in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func render(t *testing.T, p Plan) string {
	t.Helper()
	var b bytes.Buffer
	if err := RenderPlan(&b, p); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func classPlan(t *testing.T, p Plan, class string) ClassPlan {
	t.Helper()
	for _, c := range p.Classes {
		if c.Class == class {
			return c
		}
	}
	t.Fatalf("plan has no class %s: %+v", class, p.Classes)
	return ClassPlan{}
}

// codexRates give codex 14% of its weekly window for the tests class of
// planManifest: 2 codex entries x 3M high tokens x 14/6 per million.
func codexRates() map[string]Rate {
	return map[string]Rate{
		"codex":  {WeeklyPerMTok: 14.0 / 6, WindowPerMTok: 5},
		"claude": {WeeklyPerMTok: 0.5, WindowPerMTok: 2},
	}
}

// smallRates keep every plan of planManifest far below the cap.
func smallRates() map[string]Rate {
	return map[string]Rate{"codex": {WeeklyPerMTok: 0.1, WindowPerMTok: 1}, "claude": {WeeklyPerMTok: 0.1, WindowPerMTok: 1}}
}

func TestPlanHasAgent(t *testing.T) {
	p := Plan{Classes: []ClassPlan{
		{Class: "docs", Entries: []PlanEntry{{Key: "claude/sonnet/low"}}},
		{Class: "tests", Entries: []PlanEntry{{Key: "claude/opus/high"}, {Key: "codex/sol/medium", Challenger: true}}},
	}}
	if !p.HasAgent("codex") || !p.HasAgent("claude") {
		t.Fatalf("HasAgent = %v, %v; want true for codex and claude", p.HasAgent("codex"), p.HasAgent("claude"))
	}
	p.Classes[1].Entries = p.Classes[1].Entries[:1]
	if p.HasAgent("codex") {
		t.Fatal("HasAgent(codex) = true for a plan of claude entries only")
	}
}

func TestPlanEntriesLadderPlusOneChallenger(t *testing.T) {
	cfg := testConfig(t)
	p := buildPlan(t, PlanInput{Cfg: cfg, Manifest: planManifest("implement", "tests"), Rates: smallRates()})

	tests := classPlan(t, p, "tests")
	want := []PlanEntry{{Key: "codex/terra/medium"}, {Key: "codex/sol/medium"}, {Key: "claude/opus/high", Challenger: true}}
	if fmt.Sprint(tests.Entries) != fmt.Sprint(want) {
		t.Fatalf("tests entries = %+v, want %+v", tests.Entries, want)
	}
	implement := classPlan(t, p, "implement")
	if len(implement.Entries) != 3 {
		t.Fatalf("implement entries = %+v, want 3", implement.Entries)
	}
	for _, e := range implement.Entries {
		if e.Challenger {
			t.Fatalf("implement has a challenger: %+v", implement.Entries)
		}
	}
}

func TestPlanCellsAndRanges(t *testing.T) {
	cfg := testConfig(t)
	m := planManifest("tests")
	p := buildPlan(t, PlanInput{Cfg: cfg, Manifest: m, Rates: smallRates()})
	c := classPlan(t, p, "tests")
	if len(c.Tasks) != 3 || len(c.Round2) != 3 || len(c.Entries) != 3 {
		t.Fatalf("tasks %v, round 2 %v, entries %v", c.Tasks, c.Round2, c.Entries)
	}
	if c.Cells != 18 {
		t.Fatalf("Cells = %d, want 18", c.Cells)
	}
	var low, high int64
	for _, task := range m.Tasks {
		low += m.Estimates[task.ID].TokensLow
		high += m.Estimates[task.ID].TokensHigh
	}
	if c.TokensLow != low*3 || c.TokensHigh != high*3 {
		t.Fatalf("tokens = %d-%d, want %d-%d", c.TokensLow, c.TokensHigh, low*3, high*3)
	}
	if !(c.USDLow > 0 && c.USDLow < c.USDHigh) {
		t.Fatalf("USD = %v-%v", c.USDLow, c.USDHigh)
	}
	if c.Minutes != 18*(10+0.5) {
		t.Fatalf("Minutes = %v, want %v", c.Minutes, 18*(10+0.5))
	}
	if len(c.Samples) != 3 {
		t.Fatalf("Samples = %q, want 3", c.Samples)
	}
}

func TestPlanUnknownRateSaysPilot(t *testing.T) {
	cfg := testConfig(t)
	p := buildPlan(t, PlanInput{Cfg: cfg, Manifest: planManifest("tests")})
	if !p.Pilot {
		t.Fatal("Pilot = false without rates")
	}
	if !classPlan(t, p, "tests").Share["codex"].Unknown {
		t.Fatalf("Share = %+v, want codex unknown", classPlan(t, p, "tests").Share)
	}
	if out := render(t, p); !strings.Contains(out, "unknown, pilot first") {
		t.Fatalf("render lacks unknown, pilot first:\n%s", out)
	}
}

func TestPlanUnknownRateCutToBound(t *testing.T) {
	// Without a rate each cell counts 1% weekly: the 12 codex cells of tests
	// (2 entries x 6 tasks) pass the cap of 10%, so the largest round-2 task
	// goes and 10 remain. Without judges there is no judge call or challenger.
	cfg := testConfig(t)
	cfg.Learning.Judges = nil
	p := buildPlan(t, PlanInput{Cfg: cfg, Manifest: planManifest("tests")})
	c := classPlan(t, p, "tests")
	if codex := c.Share["codex"].Cells; codex != 10 {
		t.Fatalf("codex cells = %d, want 10", codex)
	}
	if len(p.Cut) != 1 || !strings.Contains(p.Cut[0], "tests-t5") {
		t.Fatalf("Cut = %q, want the largest round-2 task tests-t5", p.Cut)
	}
	if out := render(t, p); !strings.Contains(out, "total share codex: unknown, at most 10 cells before the cap of 10% weekly") {
		t.Fatalf("render lacks the cells before the cap:\n%s", out)
	}
}

func TestPlanUnknownRateCountsJudgeCallsAtBound(t *testing.T) {
	// Without a rate a judge call counts the bound scaled by its tokens, so
	// no agent's cells and judge calls together pass the cap of 10%.
	cfg := testConfig(t)
	p := buildPlan(t, PlanInput{Cfg: cfg, Manifest: planManifest("tests")})
	bound := map[string]float64{}
	calls := 0
	for _, c := range p.Classes {
		for agent, s := range c.Share {
			bound[agent] += s.Bound
		}
		for _, n := range c.JudgeCalls {
			calls += n
		}
	}
	if calls == 0 {
		t.Fatalf("no judge calls planned: %+v", p.Classes)
	}
	for agent, pct := range bound {
		if pct > p.CapWeekly {
			t.Fatalf("%s: cells and judge calls at the bound take %v%%, past the cap of %v%%", agent, pct, p.CapWeekly)
		}
	}
	if claude := bound["claude"]; claude == 0 {
		t.Fatalf("claude judge calls not counted: %v", bound)
	}
}

func TestPlanUnknownRateCountsSpent(t *testing.T) {
	// The cells and judge calls left fit in what the run has not spent yet.
	cfg := testConfig(t)
	cfg.Learning.Judges = nil
	p := buildPlan(t, PlanInput{Cfg: cfg, Manifest: planManifest("tests"), Spent: map[string]float64{"codex": 4}})
	if codex := classPlan(t, p, "tests").Share["codex"].Cells; codex != 6 {
		t.Fatalf("codex cells = %d, want 6 after 4%% spent", codex)
	}
	if out := render(t, p); !strings.Contains(out, "total share codex: unknown, at most 6 cells before the cap") {
		t.Fatalf("render lacks the steps left before the cap:\n%s", out)
	}
}

func TestPlanCutToWeeklyCap(t *testing.T) {
	cfg := testConfig(t)
	p := buildPlan(t, PlanInput{Cfg: cfg, Manifest: planManifest("tests"), Rates: codexRates()})
	share := classPlan(t, p, "tests").Share["codex"]
	if share.WeeklyHigh > 10 {
		t.Fatalf("codex WeeklyHigh = %v, want at most 10", share.WeeklyHigh)
	}
	if len(p.Cut) == 0 || !strings.Contains(p.Cut[0], "round-2") || !strings.Contains(p.Cut[0], "tests-t5") {
		t.Fatalf("Cut = %q, want the largest round-2 task tests-t5 first", p.Cut)
	}
	if out := render(t, p); !strings.Contains(out, "cap 10% weekly") {
		t.Fatalf("render lacks cap 10%% weekly:\n%s", out)
	}
}

func TestPlanCapConfiguredDown(t *testing.T) {
	cfg := testConfig(t)
	cfg.Calibration.MaxWeeklyShare = 0.05
	rates := map[string]Rate{"codex": {WeeklyPerMTok: 7.0 / 6, WindowPerMTok: 5}, "claude": {WeeklyPerMTok: 0.5, WindowPerMTok: 2}}
	p := buildPlan(t, PlanInput{Cfg: cfg, Manifest: planManifest("tests"), Rates: rates})
	if p.CapWeekly != 5 {
		t.Fatalf("CapWeekly = %v, want 5", p.CapWeekly)
	}
	if len(p.Classes) == 0 {
		t.Fatal("the cut removed every class")
	}
	if share := classPlan(t, p, "tests").Share["codex"]; share.WeeklyHigh > 5 {
		t.Fatalf("codex WeeklyHigh = %v, want at most 5", share.WeeklyHigh)
	}
}

func TestPlanHashStable(t *testing.T) {
	cfg := testConfig(t)
	a := buildPlan(t, PlanInput{Cfg: cfg, Manifest: planManifest("tests"), Rates: codexRates()})
	b := buildPlan(t, PlanInput{Cfg: cfg, Manifest: planManifest("tests"), Rates: codexRates()})
	if a.Hash == "" || a.Hash != b.Hash {
		t.Fatalf("hashes %q and %q, want the same", a.Hash, b.Hash)
	}
	if a.Hash != a.ComputeHash() {
		t.Fatalf("Hash %q, ComputeHash %q", a.Hash, a.ComputeHash())
	}
	m := planManifest("tests")
	m.Tasks = m.Tasks[1:]
	c := buildPlan(t, PlanInput{Cfg: cfg, Manifest: m, Rates: codexRates()})
	if c.Hash == a.Hash {
		t.Fatal("removing a task kept the hash")
	}
}

// The plan's hash covers the judges, in order with model and effort, and the
// rubrics they judge by.
func TestPlanHashCoversJudges(t *testing.T) {
	cfg := testConfig(t)
	base := buildPlan(t, PlanInput{Cfg: cfg, Manifest: planManifest("tests"), Rates: codexRates()})
	for name, change := range map[string]func(cfg *config.Config){
		"order":  func(cfg *config.Config) { slices.Reverse(cfg.Learning.Judges) },
		"effort": func(cfg *config.Config) { cfg.Learning.Judges[1].Effort = "medium" },
		"rubric": func(cfg *config.Config) {
			class := cfg.Learning.Classes["tests"]
			class.Rubric = append(slices.Clone(class.Rubric), "kept the style")
			cfg.Learning.Classes["tests"] = class
		},
	} {
		changed := *cfg
		changed.Learning.Judges = slices.Clone(cfg.Learning.Judges)
		changed.Learning.Classes = maps.Clone(cfg.Learning.Classes)
		change(&changed)
		if p := buildPlan(t, PlanInput{Cfg: &changed, Manifest: planManifest("tests"), Rates: codexRates()}); p.Hash == base.Hash {
			t.Errorf("a changed judge %s kept the plan's hash", name)
		}
	}
}

// Without a rate a judge call counts the bound scaled by its tokens over a
// cell's, and the swapped order only for an expected share of the pairs, so
// the default tests class keeps its cross-vendor challenger on a first plan.
func TestPlanUnknownRateKeepsChallenger(t *testing.T) {
	cfg := testConfig(t)
	m := planManifest("tests")
	m.Tasks, m.Round2 = m.Tasks[:3], nil
	p := buildPlan(t, PlanInput{Cfg: cfg, Manifest: m})
	c := classPlan(t, p, "tests")
	if len(c.Entries) != 3 || c.Cells != 9 || len(p.Cut) != 0 {
		t.Fatalf("entries %+v, %d cells, cut %q; want the challenger kept and 9 cells", c.Entries, c.Cells, p.Cut)
	}
}

func TestPlanRenderShowsEveryPart(t *testing.T) {
	cfg := testConfig(t)
	p := buildPlan(t, PlanInput{Cfg: cfg, Manifest: planManifest("tests"), Rates: codexRates()})
	out := render(t, p)
	for _, want := range []string{"cells", "tokens", "USD", "share", "wall time", "stop rules", "contamination", "judges",
		"sample statements", "repository", "history", "check time", "estimate",
		"approve with: duolog calibrate plan --approve " + p.Hash} {
		if !strings.Contains(out, want) {
			t.Fatalf("render lacks %q:\n%s", want, out)
		}
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// The plan costs the judge calls and rewrites as well as the cells. In tests,
// terra and sol pairs are judged by claude, and each pair with the opus
// challenger by both vendors: per task claude 3 calls and codex 2. The
// swapped order is costed for half the tasks, rounded up, and shown as the
// most the class can take.
func TestPlanCostsJudgeCallsAndRewrites(t *testing.T) {
	cfg := testConfig(t)
	m := planManifest("tests")
	m.Rewrite = []string{"tests-t1"}
	p := buildPlan(t, PlanInput{Cfg: cfg, Manifest: m, Rates: smallRates()})
	c := classPlan(t, p, "tests")
	if c.JudgeCalls["claude"] != 27 || c.JudgeCalls["codex"] != 18 || c.Rewrites != 1 {
		t.Fatalf("judge calls %v, rewrites %d; want claude 27, codex 18 and 1 rewrite", c.JudgeCalls, c.Rewrites)
	}
	if c.JudgeCallsMax["claude"] != 36 || c.JudgeCallsMax["codex"] != 24 {
		t.Fatalf("most judge calls %v, want claude 36, codex 24", c.JudgeCallsMax)
	}
	if c.JudgeTokensHigh != 46*judgeTokensHigh || c.JudgeTokensLow != 46*judgeTokensLow {
		t.Fatalf("judge tokens %d-%d, want 46 calls", c.JudgeTokensLow, c.JudgeTokensHigh)
	}
	// claude: the challenger's 6 cells of 3M high tokens in all, 27 judge
	// calls and the rewrite, at 0.1% weekly per million tokens.
	want := 0.1 * (3.0 + 28*float64(judgeTokensHigh)/1e6)
	if got := c.Share["claude"].WeeklyHigh; !near(got, want) {
		t.Fatalf("claude WeeklyHigh %v, want %v", got, want)
	}
	out := render(t, p)
	for _, want := range []string{"judge calls: 45 costed, up to 60 if every pair is judged again in swapped order", "rewrites:  1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render lacks %q:\n%s", want, out)
		}
	}
}

// The plan reserves each agent's judge share, the weekly points of its judge
// calls and rewrites, and shows it.
func TestPlanReservesJudgeShare(t *testing.T) {
	cfg := testConfig(t)
	m := planManifest("tests")
	m.Rewrite = []string{"tests-t1"}
	p := buildPlan(t, PlanInput{Cfg: cfg, Manifest: m, Rates: smallRates()})
	perCall := 0.1 * judgeTokensHigh / 1e6
	if got, want := p.JudgeShare["claude"], 28*perCall; !near(got, want) {
		t.Fatalf("claude judge share %v, want 27 judge calls and a rewrite, %v", got, want)
	}
	if got, want := p.JudgeShare["codex"], 18*perCall; !near(got, want) {
		t.Fatalf("codex judge share %v, want 18 judge calls, %v", got, want)
	}
	if out := render(t, p); !strings.Contains(out, "judge share claude: 0.2% weekly, kept for judge calls and rewrites") {
		t.Fatalf("render lacks the judge share:\n%s", out)
	}
}

// Judge calls count toward the cap: the cells alone fit, with the judge calls
// the largest round-2 task is cut.
func TestPlanCutCountsJudgeCalls(t *testing.T) {
	cfg := testConfig(t)
	rates := map[string]Rate{"codex": {WeeklyPerMTok: 1.5, WindowPerMTok: 1}, "claude": {WeeklyPerMTok: 0.1, WindowPerMTok: 1}}
	p := buildPlan(t, PlanInput{Cfg: cfg, Manifest: planManifest("tests"), Rates: rates})
	if len(p.Cut) != 1 || !strings.Contains(p.Cut[0], "tests-t5") {
		t.Fatalf("Cut = %q, want the round-2 task tests-t5", p.Cut)
	}
	// 2 codex entries on 2.4M high tokens and 16 codex judge calls: 5 tasks
	// and 3 swapped for each of the 2 pairs with the challenger.
	if got, want := classPlan(t, p, "tests").Share["codex"].WeeklyHigh, 1.5*(4.8+16*float64(judgeTokensHigh)/1e6); !near(got, want) {
		t.Fatalf("codex WeeklyHigh %v, want %v", got, want)
	}
}

// The run's spend so far counts toward the cap of the plan approved again.
func TestPlanCountsSpent(t *testing.T) {
	cfg := testConfig(t)
	if p := buildPlan(t, PlanInput{Cfg: cfg, Manifest: planManifest("tests"), Rates: smallRates()}); len(p.Cut) != 0 {
		t.Fatalf("Cut = %q without spend", p.Cut)
	}
	p := buildPlan(t, PlanInput{Cfg: cfg, Manifest: planManifest("tests"), Rates: smallRates(), Spent: map[string]float64{"codex": 9.9}})
	if len(p.Cut) == 0 {
		t.Fatal("a run that spent 9.9% of codex's week was not cut")
	}
	if out := render(t, p); !strings.Contains(out, "spent so far: codex 9.9% weekly") {
		t.Fatalf("render lacks the spend so far:\n%s", out)
	}
}

// Planning again after cells ran keeps their actual tokens and scales the
// cells still to run by how far the done ones went past their estimate.
func TestPlanEstimatesRemainingFromObserved(t *testing.T) {
	cfg := testConfig(t)
	m := planManifest("tests")
	first := buildPlan(t, PlanInput{Cfg: cfg, Manifest: m, Rates: smallRates()})
	cells := Cells(first, 1)
	done := 0
	for i, c := range cells {
		if c.Task == "tests-t1" && done < 2 {
			cells[i].State, cells[i].Tokens = CellDone, 1_000_000 // twice its 500k estimate
			done++
		}
	}
	p := buildPlan(t, PlanInput{Cfg: cfg, Manifest: m, Rates: smallRates(), Cells: cells})
	c := classPlan(t, p, "tests")
	if c.Done != 2 || !near(p.Observed, 2) {
		t.Fatalf("Done %d, Observed %v; want 2 and 2", c.Done, p.Observed)
	}
	// 3 entries on 9M high tokens, less the 1M the done cells were estimated
	// at, doubled, plus the done cells' 2M.
	if want := int64(2*8_000_000 + 2_000_000); c.TokensHigh != want {
		t.Fatalf("TokensHigh %d, want %d", c.TokensHigh, want)
	}
	if out := render(t, p); !strings.Contains(out, "cells:     18 (2 done)") || !strings.Contains(out, "2.0 x their high estimate") {
		t.Fatalf("render lacks the done cells and the observed factor:\n%s", out)
	}
}

// Approving a new plan keeps the cells that ran, with their ids, and adds
// pending cells only for work still to do; the plan approved already is
// refused.
func TestApproveKeepsFinishedCells(t *testing.T) {
	cfg := testConfig(t)
	m := planManifest("tests")
	first := buildPlan(t, PlanInput{Cfg: cfg, Manifest: m, Rates: smallRates()})
	run := &Run{ID: "cal-test", Step: "plan", Seed: 7}
	if err := Approve(run, first, testTime); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(run.Cells) != fmt.Sprint(Cells(first, 7)) {
		t.Fatal("the first approval did not create the plan's cells")
	}
	run.Cells[0].State, run.Cells[0].Tokens = CellDone, 400000
	run.Cells[1].State, run.Cells[1].Reason = CellFailedLimit, "provider limit"
	kept := []Cell{run.Cells[0], run.Cells[1]}
	run.Stopped = stopReapprove

	// The new plan leaves out round 2.
	m2 := planManifest("tests")
	m2.Tasks, m2.Round2 = m2.Tasks[:3], nil
	second := buildPlan(t, PlanInput{Cfg: cfg, Manifest: m2, Rates: smallRates(), Cells: run.Cells})
	if err := Approve(run, second, testTime); err != nil {
		t.Fatal(err)
	}
	if run.Approved == nil || run.Approved.Hash != second.Hash || run.Stopped != "" {
		t.Fatalf("approval %+v, stopped %q", run.Approved, run.Stopped)
	}
	ids := map[string]bool{}
	planned := map[string]int{}
	for _, c := range run.Cells {
		if ids[c.ID] {
			t.Fatalf("cell id %s twice", c.ID)
		}
		ids[c.ID] = true
		if c.Round == 2 {
			if c.State != CellSkipped || c.Reason != "not in the approved plan" {
				t.Fatalf("round-2 cell %+v, want skipped", c)
			}
			continue
		}
		planned[c.Task+" "+c.Entry]++
	}
	if len(planned) != 9 {
		t.Fatalf("%d round-1 cells, want 9: %v", len(planned), planned)
	}
	for key, n := range planned {
		if n != 1 {
			t.Fatalf("%s has %d cells", key, n)
		}
	}
	for _, want := range kept {
		i := slices.IndexFunc(run.Cells, func(c Cell) bool { return c.ID == want.ID })
		if i < 0 || fmt.Sprint(run.Cells[i]) != fmt.Sprint(want) {
			t.Fatalf("cell %s changed: %+v", want.ID, run.Cells)
		}
	}
	if err := Approve(run, second, testTime); err == nil || !strings.Contains(err.Error(), "already approved") {
		t.Fatalf("err = %v, want the approved plan refused", err)
	}
}

func TestCellsInterleavedAndFixed(t *testing.T) {
	cfg := testConfig(t)
	p := buildPlan(t, PlanInput{Cfg: cfg, Manifest: planManifest("implement", "tests"), Rates: smallRates()})
	cells := Cells(p, 7)
	if len(cells) != 36 {
		t.Fatalf("len(cells) = %d, want 36", len(cells))
	}
	for i, c := range cells {
		if want := fmt.Sprintf("c%03d", i+1); c.ID != want {
			t.Fatalf("cell %d ID = %q, want %q", i, c.ID, want)
		}
		if c.State != CellPending {
			t.Fatalf("cell %s state = %q", c.ID, c.State)
		}
		wantRound := 1
		if i >= 18 {
			wantRound = 2
		}
		if c.Round != wantRound {
			t.Fatalf("cell %s round = %d, want %d", c.ID, c.Round, wantRound)
		}
		wantClass := "implement"
		if i%2 == 1 {
			wantClass = "tests"
		}
		if c.Class != wantClass {
			t.Fatalf("cell %s class = %q, want %q", c.ID, c.Class, wantClass)
		}
	}
	if fmt.Sprint(Cells(p, 7)) != fmt.Sprint(cells) {
		t.Fatal("the same seed gave another order")
	}
	if fmt.Sprint(Cells(p, 8)) == fmt.Sprint(cells) {
		t.Fatal("another seed gave the same order")
	}
}
