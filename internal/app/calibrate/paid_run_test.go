package calibration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/app/paidruns/paidrunstest"
	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// sharedGate makes every delegation of the test admit through one gate, as
// one user's runs do.
func sharedGate(t *testing.T, cfg *config.Config) *paidruns.Gate {
	t.Helper()
	g, err := paidrunstest.Gate(filepath.Join(t.TempDir(), "accounts"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	old := realDeps.PaidGate
	realDeps.PaidGate = func(*config.Config) (*paidruns.Gate, error) { return g, nil }
	t.Cleanup(func() { realDeps.PaidGate = old })
	return g
}

func gateState(t *testing.T, g *paidruns.Gate) paidruns.State {
	t.Helper()
	var st paidruns.State
	if err := g.Store.View(context.Background(), func(s paidruns.State) error { st = s; return nil }); err != nil {
		t.Fatal(err)
	}
	return st
}

// fakeAccounts records the shared rate observations and answers which step
// receipts still hold an account reservation.
type fakeAccounts struct {
	observed   []string
	held       map[string]bool
	reconciled int
}

func (a *fakeAccounts) Reconcile(context.Context) error { a.reconciled++; return nil }
func (a *fakeAccounts) Held(_ context.Context, receipt string) (bool, error) {
	return a.held[receipt], nil
}
func (a *fakeAccounts) Observe(_ context.Context, agent, family string, input, cached, output int64) error {
	a.observed = append(a.observed, strings.Join([]string{agent, family,
		strconv.FormatInt(input, 10), strconv.FormatInt(cached, 10), strconv.FormatInt(output, 10)}, " "))
	return nil
}

// budgetCells makes the fixture's cells record the step budget each one
// runs under and pass with a measured split of their tokens.
func (f *runFixture) budgetCells(budgets *[]paidrules.StepBudget) {
	f.runner.Cell = func(ctx context.Context, c Cell, task TaskRecord, start StartFunc) (CellResult, error) {
		if err := StartStep(start); err != nil {
			return CellResult{}, err
		}
		if b, _ := StepOptions(ctx); b != nil {
			*budgets = append(*budgets, *b)
		}
		f.calls = append(f.calls, "cell:"+task.ID)
		return CellResult{State: CellDone, Passed: true, Tokens: runEstimate, Input: 900000, Cached: 300000, Output: 100000, USD: 1, Seconds: 120}, nil
	}
}

// paidCodex bills codex as a paid route with both opt-ins.
func paidCodex(approved, cap, spent float64) map[string]BillingBudget {
	return map[string]BillingBudget{"codex": {Verdict: VerdictPaid, Account: "personal", UsableShare: 1,
		CapUSD: cap, SpentUSD: spent, ApprovedUSD: approved, Allow: true, Calibrate: true}}
}

// Every actual worker run of a calibration step takes its own account
// permit: a cell runs its delegation in delegate's worker process, after
// its isolation and trust setup and its StartFunc, and settles once in the
// shared ledger; a step refused before it starts holds and spends nothing.
// No step starts without the billing opt-ins or with a plan other than the
// approved one. Cells cannot spend what the plan holds for judge calls and
// rewrites, and a run resumed after a crash takes a new receipt, never an
// uncharged rerun of the old one. (Each attempt of a login retry takes its
// own permit: TestAttemptSettlementOnCancelRetryAndFallback.)
func TestCalibrationOnePermitPerActualAttempt(t *testing.T) {
	t.Run("a real cell takes one permit after its start", func(t *testing.T) {
		f := newCellFixture(t)
		g := sharedGate(t, f.env.Cfg)
		var out delegate.Outcome
		fake := f.realClaude(t, testbin.Scenario{Stdout: parseAnswer, Writes: map[string]string{"parse.go": "package widget\n\nfunc Parse(line string) []string { return nil }\n"}}, &out)
		ctx, step := withStep(context.Background())
		start := func() (bool, error) {
			step.budget = &paidrules.StepBudget{PlanHash: "plan-h", StepID: "c-1.1", USDRemaining: 10, WeeklyRemaining: 0.1, WindowRemaining: 0.5, WaitForSession: true}
			return true, nil
		}
		r, err := RunCell(ctx, f.env, f.cell, f.task, start)
		if err != nil || r.State != CellDone {
			t.Fatalf("cell: %+v %v (%q)", r, err, out.Line)
		}
		st := gateState(t, g)
		if len(st.Reservations) != 0 || len(st.Settled) != 1 {
			t.Fatalf("one permit settled once: %+v %+v", st.Reservations, st.Settled)
		}
		for _, s := range st.Settled {
			if !s.Started || s.Input == 0 {
				t.Fatalf("settlement: %+v", s)
			}
		}
		if r.Input == 0 || r.Output == 0 {
			t.Fatalf("the cell lost its token split: %+v", r)
		}
		calls := callCount(t, fake)

		refused := func() (bool, error) { return false, nil }
		if _, err := RunCell(ctx, f.env, f.cell, f.task, refused); !errors.Is(err, ErrNotAdmitted) {
			t.Fatalf("refused start: %v", err)
		}
		if st := gateState(t, g); len(st.Settled) != 1 || len(st.Reservations) != 0 || callCount(t, fake) != calls {
			t.Fatal("a refused step reserved, spent or ran")
		}
	})

	t.Run("no start without opt-ins or the approved plan", func(t *testing.T) {
		f := newRunFixture(t, []string{"t1"}, []string{"codex/sol/medium"})
		var budgets []paidrules.StepBudget
		f.budgetCells(&budgets)
		billing := paidCodex(10, 100, 0)
		b := billing["codex"]
		b.Calibrate = false
		f.runner.Billing = map[string]BillingBudget{"codex": b}
		if o := f.execute(t); o.Stopped != "paid calibration not allowed" || len(f.calls) != 0 {
			t.Fatalf("outcome %+v, calls %v", o, f.calls)
		}
		f.runner.Billing = nil
		f.runner.Plan.Hash = "another-plan"
		if _, err := f.runner.Execute(context.Background()); err == nil || len(f.calls) != 0 {
			t.Fatalf("a plan other than the approved one ran: %v %v", err, f.calls)
		}
	})

	t.Run("cells leave the judge USD", func(t *testing.T) {
		f := newRunFixture(t, []string{"t1"}, []string{"codex/sol/medium"})
		var budgets []paidrules.StepBudget
		f.budgetCells(&budgets)
		f.runner.Plan.Hash, f.run.Approved.Hash = "h", "h"
		f.runner.Plan.Classes[0].JudgeUSD = map[string]float64{"codex": 4}
		f.runner.Billing = paidCodex(10, 100, 0)
		if o := f.execute(t); !o.Done {
			t.Fatalf("outcome %+v\n%s", o, f.out.String())
		}
		if len(budgets) != 1 || budgets[0].USDRemaining != 6 || budgets[0].PlanHash != "h" || !budgets[0].WaitForSession {
			t.Fatalf("cell budget: %+v", budgets)
		}
		if judge := f.runner.stepBudget("codex", "judge-1", true); judge.USDRemaining != 9 {
			t.Fatalf("judge budget after a cell of 1 USD: %+v", judge)
		}
	})

	t.Run("a resumed run takes a new receipt", func(t *testing.T) {
		f := newRunFixture(t, []string{"t1"}, []string{"codex/sol/medium"})
		accounts := &fakeAccounts{}
		f.runner.Accounts = accounts
		var budgets []paidrules.StepBudget
		crash := true
		f.runner.Cell = func(ctx context.Context, c Cell, task TaskRecord, start StartFunc) (CellResult, error) {
			if err := StartStep(start); err != nil {
				return CellResult{}, err
			}
			b, _ := StepOptions(ctx)
			budgets = append(budgets, *b)
			if crash {
				return CellResult{}, errors.New("the supervisor died")
			}
			return CellResult{State: CellDone, Passed: true, Tokens: runEstimate, Seconds: 1}, nil
		}
		if _, err := f.runner.Execute(context.Background()); err == nil {
			t.Fatal("a crashed cell succeeded")
		}
		if got := f.saved(t).Receipts[f.run.Cells[0].ID]; got != budgets[0].StepID {
			t.Fatalf("receipt %q not saved, budget %+v", got, budgets[0])
		}
		crash = false
		if o := f.execute(t); !o.Done {
			t.Fatalf("resume: %+v", o)
		}
		if len(budgets) != 2 || budgets[0].StepID == budgets[1].StepID || accounts.reconciled == 0 {
			t.Fatalf("receipts %+v, reconciled %d", budgets, accounts.reconciled)
		}
	})
}

// A full account makes a cell wait for a session with no deadline, and the
// run says so while it waits; a cancellation takes the waiter's ticket away.
// A paid route at its 24-hour cap pauses the run, saved, and a resume with
// room admits it again. A changed cap or account never widens the approved
// plan, other projects' spend counts against the same cap, and only an
// opted-in paid step needs no window; a rejected subscription family stops.
func TestCalibrationSessionWaitAndCapPause(t *testing.T) {
	t.Run("a cell waits for a session", func(t *testing.T) {
		f := newRunFixture(t, []string{"t1"}, []string{"codex/sol/medium"})
		cfg := f.runner.Cfg
		cfg.Limits.SessionsByTier = config.DefaultSessionTiers()
		cfg.Limits.SessionsByTier.Small = 1
		cfg.Limits.MaxTotalSessions = 10
		g, err := paidrunstest.Gate(filepath.Join(t.TempDir(), "accounts"), cfg)
		if err != nil {
			t.Fatal(err)
		}
		err = g.Store.Update(context.Background(), func(st *paidruns.State) error {
			st.Reservations["other"] = paidrules.Reservation{ID: "other", Agent: "codex", Shape: corelaunch.Worker,
				Variant: corelaunch.Free, Session: true, Task: true, Created: time.Now()}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		g.Reconcile = paidruns.ReconcileInput{Process: func(paidrules.Owner) paidruns.Presence { return paidruns.Alive }}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		waits := 0
		var during *Run
		g.Wait = func(ctx context.Context, d time.Duration) error {
			if waits++; waits == 90 {
				during = f.saved(t)
				if st := gateState(t, g); len(st.Tickets) != 1 || !strings.HasPrefix(st.Tickets[0].Step, "h/") {
					t.Errorf("waiting without a ticket of the step: %+v", st.Tickets)
				}
				cancel()
			}
			return ctx.Err()
		}
		f.runner.Plan.Hash, f.run.Approved.Hash = "h", "h"
		f.runner.Cell = func(ctx context.Context, c Cell, task TaskRecord, start StartFunc) (CellResult, error) {
			if err := StartStep(start); err != nil {
				return CellResult{}, err
			}
			budget, waiting := StepOptions(ctx)
			in := corelaunch.ProcessInput{Agent: "codex", Shape: corelaunch.Worker, Variant: corelaunch.Free, Config: cfg, Environ: os.Environ()}
			_, err := g.Acquire(ctx, paidruns.Request{Input: in, Family: "sol", Role: "implement", Calibration: true,
				Budget: budget, Waiting: waiting, Owner: paidrules.Owner{PID: os.Getpid(), Birth: "test"}})
			return CellResult{}, err
		}
		if _, err := f.runner.Execute(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("a cancelled wait: %v", err)
		}
		if during == nil || during.WaitReason != "waiting for a session of codex" {
			t.Fatalf("the run did not say it waits: %+v", during)
		}
		if st := gateState(t, g); len(st.Tickets) != 0 {
			t.Fatalf("a cancelled waiter kept its ticket: %+v", st.Tickets)
		}
		if f.saved(t).WaitReason != "" {
			t.Fatal("the wait reason outlived the wait")
		}
	})

	t.Run("a paid cap pauses, a resume with room goes on", func(t *testing.T) {
		f := newRunFixture(t, []string{"t1"}, []string{"codex/sol/medium"})
		f.runner.Billing = paidCodex(10, 5, 5)
		o := f.execute(t)
		if o.Pause.IsZero() || len(f.calls) != 0 || !f.saved(t).Pause.Equal(o.Pause) {
			t.Fatalf("at the cap: %+v, calls %v", o, f.calls)
		}
		f.runner.Billing = paidCodex(10, 5, 1)
		f.runner.Now = func() time.Time { return o.Pause.Add(time.Second) }
		if o := f.execute(t); !o.Done || len(f.calls) != 1 {
			t.Fatalf("resume with room: %+v, calls %v", o, f.calls)
		}
	})

	t.Run("the approved plan is never widened", func(t *testing.T) {
		p := Plan{Billing: map[string]BillingBudget{"codex": {Verdict: VerdictPaid, Account: "workspace", UsableShare: 0.5, CapUSD: 5, ApprovedUSD: 4, Allow: true, Calibrate: true}}}
		got := p.LimitBilling(map[string]BillingBudget{"codex": {Verdict: VerdictPaid, Account: "personal", UsableShare: 1, CapUSD: 50, Allow: true, Calibrate: true}})["codex"]
		if got.CapUSD != 5 || got.UsableShare != 0.5 || got.Account != "workspace" || got.ApprovedUSD != 4 {
			t.Fatalf("widened: %+v", got)
		}
	})

	t.Run("other projects' spend counts", func(t *testing.T) {
		cfg := testConfig(t)
		pa := cfg.PaidRuns.Agent("codex")
		pa.Allow, pa.DailyCapUSD, pa.Calibrate = true, 5, true
		cfg.PaidRuns.Set("codex", pa)
		g, err := paidrunstest.Gate(filepath.Join(t.TempDir(), "accounts"), cfg)
		if err != nil {
			t.Fatal(err)
		}
		g.Cache.Read = paidRoute
		err = g.Store.Update(context.Background(), func(st *paidruns.State) error {
			st.Settled["elsewhere"] = paidrules.Settlement{ID: "elsewhere", Agent: "codex", ChargedUSD: 4.9, Started: true, Ended: time.Now()}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		in := corelaunch.ProcessInput{Agent: "codex", Shape: corelaunch.Worker, Variant: corelaunch.Paid, Config: cfg, Environ: os.Environ()}
		budget := &paidrules.StepBudget{PlanHash: "h", StepID: "c001.1", USDRemaining: 100, WeeklyRemaining: 1, WindowRemaining: 1}
		_, err = g.Acquire(context.Background(), paidruns.Request{Input: in, Family: "sol", Role: "implement", Calibration: true, Budget: budget})
		var r *paidruns.Refusal
		if !errors.As(err, &r) || r.Reason != paidruns.ReasonCap {
			t.Fatalf("another project's spend did not count: %v", err)
		}
		budget.USDRemaining = 0.01
		pa = cfg.PaidRuns.Agent("codex")
		pa.DailyCapUSD = 1000
		cfg.PaidRuns.Set("codex", pa)
		if _, err := g.Acquire(context.Background(), paidruns.Request{Input: in, Family: "sol", Role: "implement", Calibration: true, Budget: budget}); !errors.As(err, &r) || r.Reason != paidrules.ReasonStepBudget {
			t.Fatalf("a step past its approved USD: %v", err)
		}
		budget.WeeklyRemaining = 2
		if _, err := g.Acquire(context.Background(), paidruns.Request{Input: in, Family: "sol", Role: "implement", Calibration: true, Budget: budget}); err == nil || errors.As(err, &r) {
			t.Fatalf("an invalid window fraction: %v", err)
		}
	})

	t.Run("windows", func(t *testing.T) {
		paid := paidCodex(10, 100, 0)["codex"]
		if d := AdmitBilled("codex", paid, nil, CellCost{WeeklyPct: 1}, Spend{}, testCalibration(), 0, testTime); !d.Admit {
			t.Fatalf("an opted-in paid step without windows: %+v", d)
		}
		sub := BillingBudget{Verdict: VerdictSubscription, Account: "personal", UsableShare: 1}
		ws := []Window{weekly(10), fiveHour(10), {Kind: KindFiveHour, Model: "sol", UsedPercent: 100, ResetsAt: testTime.Add(time.Hour)}}
		if d := AdmitBilled("codex", sub, forModel(ws, "sol"), CellCost{WeeklyPct: 1, WindowPct: 1}, Spend{}, testCalibration(), 0, testTime); d.Stop != "provider limit" {
			t.Fatalf("a rejected family: %+v", d)
		}
	})
}

// paidRoute reads a route billed per token.
func paidRoute(ctx context.Context, p corelaunch.Process) (account.Snapshot, error) {
	s, err := paidrunstest.Subscription(ctx, p)
	s.Billing.RouteSignals = []corelaunch.Signal{{Name: "auth:apiKey", Layer: "account/read"}}
	return s, err
}

// The runner keeps its local approved-run spend as it was and feeds the
// shared rate learner once per step with the step's real tokens, scaled by
// nothing; a workspace account observes the same tokens. A cell whose
// earlier attempt still holds its account reservation, such as a live
// worker of a killed supervisor, is not run again on resume.
func TestCalibrationSharedRatesAndSpendOnce(t *testing.T) {
	run := func(t *testing.T, accounts StepAccounts, billing map[string]BillingBudget) *runFixture {
		f := newRunFixture(t, []string{"t1", "t2"}, []string{"codex/sol/medium"})
		f.step = 2
		var budgets []paidrules.StepBudget
		f.budgetCells(&budgets)
		f.runner.Accounts, f.runner.Billing = accounts, billing
		if o := f.execute(t); !o.Done {
			t.Fatalf("outcome %+v\n%s", o, f.out.String())
		}
		return f
	}
	plain := run(t, nil, nil)
	accounts := &fakeAccounts{}
	shared := run(t, accounts, nil)
	if got, want := shared.saved(t).Spend.WeeklyPct, plain.saved(t).Spend.WeeklyPct; got["codex"] != want["codex"] || got["codex"] == 0 {
		t.Fatalf("local spend changed: %v, was %v", got, want)
	}
	if len(accounts.observed) != 2 || accounts.observed[0] != "codex sol 900000 300000 100000" {
		t.Fatalf("observations: %q", accounts.observed)
	}
	workspace := &fakeAccounts{}
	run(t, workspace, map[string]BillingBudget{"codex": {Verdict: VerdictSubscription, Account: "workspace", UsableShare: 0.5}})
	if strings.Join(workspace.observed, "|") != strings.Join(accounts.observed, "|") {
		t.Fatalf("a workspace account scaled its observations: %q", workspace.observed)
	}

	f := newRunFixture(t, []string{"t1"}, []string{"codex/sol/medium"})
	var budgets []paidrules.StepBudget
	f.budgetCells(&budgets)
	id := f.run.Cells[0].ID
	f.run.Cells[0].State = CellRunning
	f.run.Receipts = map[string]string{id: id + ".1"}
	f.runner.Accounts = &fakeAccounts{held: map[string]bool{f.runner.receipt(id + ".1"): true}}
	o := f.execute(t)
	if len(f.calls) != 0 || o.Stopped != stopHeld || !strings.Contains(f.out.String(), "still holds its account reservation") {
		t.Fatalf("a held cell ran again: %+v %v\n%s", o, f.calls, f.out.String())
	}
}

// callCount counts the calls of a fake binary.
func callCount(t *testing.T, bin string) int {
	t.Helper()
	data, err := os.ReadFile(bin + ".calls.ndjson")
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}
