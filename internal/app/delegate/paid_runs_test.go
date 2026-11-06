package delegate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/auth"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/run"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// accounts is the account state of the harness's gate.
func (h *harness) accounts() paidruns.State {
	h.t.Helper()
	var st paidruns.State
	if err := h.gate.Store.Update(context.Background(), func(s *paidruns.State) error { st = *s; return nil }); err != nil {
		h.t.Fatal(err)
	}
	return st
}

// settled lists the settlements in the order the runs ended.
func (h *harness) settled() []paidruns.Settlement {
	h.t.Helper()
	var out []paidruns.Settlement
	for _, s := range h.accounts().Settled {
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b paidruns.Settlement) int { return a.Ended.Compare(b.Ended) })
	return out
}

// paidReads makes the quota reads of agents show an API key route.
func (h *harness) paidReads(agents ...string) {
	read := h.gate.Cache.Read
	h.gate.Cache.Read = func(ctx context.Context, p workerenv.Process) (quota.Snapshot, error) {
		s, err := read(ctx, p)
		if slices.Contains(agents, p.Agent) {
			s.Billing.RouteSignals = []workerenv.Signal{{Name: "auth:apiKey", Layer: "auth status"}}
		}
		return s, err
	}
}

// wantSettledOnce checks that every run held its own reservation, attached
// its child before it ran on, and settled once, and that nothing is held.
func (h *harness) wantSettledOnce(runs int) []paidruns.Settlement {
	h.t.Helper()
	h.requireCalls(runs)
	ids := map[string]bool{}
	for i, held := range h.held {
		if !held.Started || !held.Launching || held.Child.PID != os.Getpid() || held.Supervisor.PID != os.Getpid() {
			h.t.Fatalf("run %d ran without its child attached: %+v", i, held)
		}
		ids[held.ID] = true
	}
	st := h.accounts()
	if len(st.Reservations) != 0 {
		h.t.Fatalf("reservations still held: %+v", st.Reservations)
	}
	settled := h.settled()
	if len(settled) != len(h.held) || len(ids) != len(h.held) {
		h.t.Fatalf("%d runs with %d reservations settled %d times", len(h.held), len(ids), len(settled))
	}
	for _, s := range settled {
		if !ids[s.ID] || !s.Started {
			h.t.Fatalf("settlement of no run: %+v", s)
		}
	}
	return settled
}

// Each actual worker run takes its own permit, attaches its child before it
// runs on and settles exactly once with what it measured: a cancelled run, a
// login retry, a quota fallback, a failed answer and a turn cap alike. A
// retry acquires again, under the cap as it is then. A run that never
// started releases its hold uncharged, and a child whose attach failed is
// never freed.
func TestAttemptSettlementOnCancelRetryAndFallback(t *testing.T) {
	t.Run("one permit per run", func(t *testing.T) {
		h := newHarness(t)
		if _, err := h.run("implement"); err != nil {
			t.Fatal(err)
		}
		s := h.wantSettledOnce(1)[0]
		if s.Input != 12 || s.Output != 3 || s.ChargedUSD != 0 || s.Agent != h.calls[0].Route.Agent {
			t.Fatalf("settlement: %+v", s)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		h := newHarness(t)
		h.results = []run.Result{{Interrupted: true, Usage: run.Usage{Input: 50, Output: 5}}}
		h.errs = []error{context.Canceled}
		if _, err := h.run("implement"); err == nil {
			t.Fatal("a cancelled run succeeded")
		}
		s := h.wantSettledOnce(1)[0]
		if s.Input != 50 || s.CompleteUsage {
			t.Fatalf("cancelled settlement: %+v", s)
		}
	})

	t.Run("login retry keeps the first run's measurements", func(t *testing.T) {
		h := newHarness(t)
		reported := 0.25
		h.results = []run.Result{{AuthFailed: true, Usage: run.Usage{Input: 100, Cached: 40, Output: 10}, UsageKnown: true,
			UsageComplete: true, ReportedUSD: &reported, Billing: quota.RunEvidence{Expected: true, Seen: true, UsingOverage: true}}}
		h.auth.login = auth.LoggedIn
		o, err := h.run("implement")
		if err != nil || !strings.HasPrefix(o.Line, "done=true") {
			t.Fatal(o.Line, err)
		}
		settled := h.wantSettledOnce(2)
		first, second := settled[0], settled[1]
		if first.Input != 100 || first.Cached != 40 || first.ReportedUSD == nil || *first.ReportedUSD != reported ||
			!first.Evidence.UsingOverage || !first.CompleteUsage || first.ChargedUSD < reported {
			t.Fatalf("the first run's measurements were lost: %+v", first)
		}
		if second.Input != 12 || second.Evidence.UsingOverage {
			t.Fatalf("second settlement: %+v", second)
		}
	})

	t.Run("quota fallback", func(t *testing.T) {
		h := newHarness(t)
		h.results = []run.Result{{QuotaEnded: true, Usage: run.Usage{Input: 30, Output: 3}, UsageKnown: true}}
		if _, err := h.run("implement"); err != nil {
			t.Fatal(err)
		}
		settled := h.wantSettledOnce(2)
		if settled[0].Agent == settled[1].Agent || settled[0].Input != 30 {
			t.Fatalf("fallback settlements: %+v", settled)
		}
	})

	t.Run("failed answer and turn cap", func(t *testing.T) {
		for _, c := range []struct {
			result run.Result
			err    error
		}{
			{run.Result{Usage: run.Usage{Input: 70, Output: 7}, UsageKnown: true}, errors.New("invalid answer.json")},
			{run.Result{TurnsEnded: true, Usage: run.Usage{Input: 80, Output: 8}, UsageKnown: true}, run.ErrTurnCap},
		} {
			h := newHarness(t)
			h.results, h.errs = []run.Result{c.result}, []error{c.err}
			if _, err := h.run("implement"); err == nil {
				t.Fatal("a failed run succeeded")
			}
			if s := h.wantSettledOnce(1)[0]; s.Input != c.result.Usage.Input {
				t.Fatalf("%v: %+v", c.err, s)
			}
		}
	})

	t.Run("never started", func(t *testing.T) {
		h := newHarness(t)
		h.skipStart = map[int]bool{0: true}
		h.errs = []error{&os.PathError{Op: "fork/exec", Path: "codex", Err: os.ErrNotExist}}
		if _, err := h.run("implement"); err != nil {
			t.Fatal(err)
		}
		// The first run never started: no settlement, nothing held.
		h.requireCalls(2)
		if settled := h.settled(); len(settled) != 1 || settled[0].Agent != h.calls[1].Route.Agent {
			t.Fatalf("an unstarted run was settled: %+v", settled)
		}
		if held := h.accounts().Reservations; len(held) != 0 {
			t.Fatalf("an unstarted hold stayed: %+v", held)
		}
	})

	t.Run("attach fails after the start", func(t *testing.T) {
		h := newHarness(t)
		state := filepath.Join(h.gate.Store.Dir, "state.json")
		var saved []byte
		h.beforeStart = func() {
			var err error
			if saved, err = os.ReadFile(state); err != nil {
				t.Fatal(err)
			}
			// The account state cannot be written while the child starts.
			put(t, state, "not json")
		}
		h.errs = []error{nil}
		_, err := h.run("implement")
		if err == nil {
			t.Fatal("a run whose child was never recorded succeeded")
		}
		put(t, state, string(saved))
		held := h.accounts().Reservations
		if len(held) != 1 {
			t.Fatalf("the hold of a live child was freed: %+v", held)
		}
		for id, r := range held {
			if !r.Launching {
				t.Fatalf("%s is not marked as launching: %+v", id, r)
			}
			// Even with the supervisor gone, a launch keeps it held.
			gone := paidruns.ReconcileInput{Process: func(paidruns.Owner) paidruns.Presence { return paidruns.Gone }}
			if err := h.gate.Store.Reconcile(context.Background(), gone); err != nil {
				t.Fatal(err)
			}
			if _, ok := h.accounts().Reservations[id]; !ok {
				t.Fatal("reconciliation freed a launched child")
			}
		}
	})

	t.Run("a retry acquires under the cap as it is then", func(t *testing.T) {
		h := newHarness(t)
		h.paidReads("codex", "claude")
		h.cfg.PaidRuns.Codex.Allow, h.cfg.PaidRuns.Codex.DailyCapUSD = true, 1000
		h.auth.login = auth.LoggedIn
		h.beforeStart = func() {
			// The first run spends its whole estimate, and the cap then
			// leaves room for half of another.
			for _, r := range h.accounts().Reservations {
				h.cfg.PaidRuns.Codex.DailyCapUSD = 1.5 * r.Estimate.USD
				usd := r.Estimate.USD
				h.results = []run.Result{{AuthFailed: true, ReportedUSD: &usd, Usage: run.Usage{Input: 10, Output: 1}, UsageKnown: true}}
			}
			h.beforeStart = nil
		}
		o, err := h.run("implement")
		if err != nil {
			t.Fatal(err)
		}
		h.requireCalls(1)
		if o.RunHere == nil || !strings.HasPrefix(o.Line, "implement -> lead: ") || !strings.Contains(o.Line, "daily cap") {
			t.Fatalf("the retry was not refused at the cap: %q", o.Line)
		}
		if h.calls[0].Process.Variant != workerenv.Paid || h.calls[0].Process.Agent != "codex" {
			t.Fatalf("paid run: %+v", h.calls[0].Process)
		}
		if settled := h.settled(); len(settled) != 1 || settled[0].ChargedUSD <= 0 {
			t.Fatalf("the paid run was not charged once: %+v", settled)
		}
	})

	t.Run("refused everywhere goes to the lead", func(t *testing.T) {
		h := newHarness(t)
		h.paidReads("codex", "claude")
		o, err := h.run("implement")
		if err != nil {
			t.Fatal(err)
		}
		h.requireCalls(0)
		if o.RunHere == nil || !strings.HasPrefix(o.Line, "implement -> lead: codex worker is paid") ||
			!strings.Contains(o.Line, "paid_runs.codex") || !strings.Contains(o.Line, "claude worker is paid") {
			t.Fatalf("lead line: %q", o.Line)
		}
		if st := h.accounts(); len(st.Reservations) != 0 || len(st.Settled) != 0 {
			t.Fatalf("a refusal held or charged: %+v", st)
		}
		// A calibration step refused this way never started.
		_, err = h.runWith("implement", Options{Calibration: true})
		var notStarted *NotStartedError
		if !errors.As(err, &notStarted) {
			t.Fatalf("calibration refusal: %v", err)
		}
	})
}

// A free candidate refused, whose fallback is the lead's own agent, does not
// skip the paid variant the refused agent allows: the paid pass runs before
// the task goes to the lead. When the paid pass is refused too, or no paid
// pass exists, the task goes to the lead in the fallback's placement, and the
// line says why each candidate was refused.
func TestInLeadFallbackKeepsPaidPassAndRefusals(t *testing.T) {
	t.Run("paid pass runs before the lead", func(t *testing.T) {
		h := newHarness(t)
		h.lead("claude", mode.Ask, "high")
		h.paidReads("codex")
		h.cfg.PaidRuns.Codex.Allow, h.cfg.PaidRuns.Codex.DailyCapUSD = true, 5
		o, err := h.run("implement")
		if err != nil {
			t.Fatal(err)
		}
		if o.RunHere != nil {
			t.Fatalf("the task went to the lead before the paid pass: %q", o.Line)
		}
		h.requireCalls(1)
		if p := h.calls[0].Process; p.Agent != "codex" || p.Variant != workerenv.Paid {
			t.Fatalf("run: %+v", p)
		}
	})

	for _, c := range []struct {
		name  string
		allow bool
	}{{"paid pass refused", true}, {"no paid pass", false}} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.lead("claude", mode.Ask, "high")
			h.paidReads("codex")
			if c.allow {
				h.cfg.PaidRuns.Codex.Allow, h.cfg.PaidRuns.Codex.DailyCapUSD = true, 0.000001
			}
			o, err := h.run("implement")
			if err != nil {
				t.Fatal(err)
			}
			h.requireCalls(0)
			if o.RunHere == nil || !strings.HasPrefix(o.Line, "implement -> lead: codex worker is paid") {
				t.Fatalf("lead line: %q", o.Line)
			}
			if o.RunHere.Model != "sonnet" {
				t.Fatalf("not the fallback's placement: %#v", *o.RunHere)
			}
			if c.allow && !strings.Contains(o.Line, "codex daily cap reached") {
				t.Fatalf("the paid refusal is missing: %q", o.Line)
			}
		})
	}
}

// Every worker path builds its process once, after the route, the context
// and the bridged servers are known: fresh, resumed, an explicit target and
// a judge. The free process withholds listed API keys and carries the
// protected roots.
func TestWorkerBuildGatesExplicitPassthroughInDelegate(t *testing.T) {
	h := newHarness(t)
	t.Setenv("OPENAI_API_KEY", "sk-delegate-secret")
	h.cfg.EnvPassthrough = append(h.cfg.EnvPassthrough, "OPENAI_API_KEY")
	h.cfg.PaidRuns.Codex.Allow, h.cfg.PaidRuns.Codex.DailyCapUSD = true, 5
	roots := []string{filepath.Join(h.root, "protected")}
	target := config.Target{Agent: "claude", Model: "sonnet", Effort: "high"}
	for _, opts := range []Options{
		{ProtectedRoots: roots},
		{ProtectedRoots: roots, Entry: &target, Person: true},
		{ProtectedRoots: roots, Entry: &target, Judge: true},
	} {
		if _, err := h.runWith("implement", opts); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.runWith("plan", Options{ProtectedRoots: roots}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.runWith("plan", Options{ProtectedRoots: roots}); err != nil {
		t.Fatal(err)
	}
	h.requireCalls(5)
	if h.calls[4].ThreadID == "" {
		t.Fatal("the plan thread was not resumed")
	}
	for i, s := range h.calls {
		p := s.Process
		if p == nil || p.Agent != s.Route.Agent || p.Variant != workerenv.Free || p.Shape != workerenv.Worker {
			t.Fatalf("run %d: %+v", i, p)
		}
		if slices.ContainsFunc(p.Env, func(pair string) bool { return strings.HasPrefix(pair, "OPENAI_API_KEY=") }) {
			t.Fatalf("run %d got a listed API key", i)
		}
		if !slices.ContainsFunc(p.Env, func(pair string) bool { return strings.HasPrefix(pair, "DUOLOG_RUN=") }) {
			t.Fatalf("run %d lost its run id", i)
		}
		if p.Agent == "claude" && !strings.Contains(p.Settings, "protected") {
			t.Fatalf("run %d settings lack the protected roots: %s", i, p.Settings)
		}
	}
}

// The login check of a worker is made on the very process the worker runs.
func TestAgentAuthUsesWorkerProcessInDelegate(t *testing.T) {
	h := newHarness(t)
	if _, err := h.run("implement"); err != nil {
		t.Fatal(err)
	}
	h.requireCalls(1)
	if len(h.checked) == 0 || h.checked[len(h.checked)-1].CacheKey != h.calls[0].Process.CacheKey {
		t.Fatalf("checked %d processes, not the worker's", len(h.checked))
	}
}
