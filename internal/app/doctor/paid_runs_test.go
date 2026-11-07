package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/app/paidruns/paidrunstest"
	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
)

// Values that must never reach a report: keys, a lead's key, an endpoint URL.
const (
	reportEndpoint = "https://llm.example.com/claude"
	reportKey      = "sk-openai-report-secret"
	reportLeadKey  = "sk-ant-lead-secret"
)

// reportGate makes delegate.NewGate return one gate over a temporary account
// state, whose reads answer as read does.
func reportGate(t *testing.T, cfg *config.Config, read func(context.Context, corelaunch.Process) (account.Snapshot, error)) *paidruns.Gate {
	t.Helper()
	g, err := paidrunstest.Gate(t.TempDir(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if read != nil {
		g.Cache.Read = read
	}
	old := testDeps.PaidGate
	testDeps.PaidGate = func(*config.Config) (*paidruns.Gate, error) { return g, nil }
	t.Cleanup(func() { testDeps.PaidGate = old })
	return g
}

// shapeReads answers Claude as a first-party subscription with paid overage
// on, and Codex with an API key route.
func shapeReads(ctx context.Context, p corelaunch.Process) (account.Snapshot, error) {
	s, err := paidrunstest.Subscription(ctx, p)
	switch p.Agent {
	case "claude":
		s.Billing.OverageEnabled = true
	case "codex":
		s.Billing.RouteSignals = []corelaunch.Signal{{Name: "auth:apiKey", Layer: "account/read"}}
	}
	return s, err
}

// seed puts spend, runs in flight and live sessions in the account state.
func seed(t *testing.T, g *paidruns.Gate) {
	t.Helper()
	later := time.Now().Add(time.Minute)
	err := g.Store.Update(context.Background(), func(st *paidruns.State) error {
		st.Reservations["claude-live"] = paidrules.Reservation{ID: "claude-live", Agent: "claude", Shape: corelaunch.Worker,
			Variant: corelaunch.Free, Session: true, Task: true, Created: time.Now()}
		st.Reservations["codex-paid"] = paidrules.Reservation{ID: "codex-paid", Agent: "codex", Shape: corelaunch.Worker,
			Variant: corelaunch.Paid, Paid: true, Session: true, Task: true, Created: time.Now(),
			Estimate: paidrules.Estimate{USD: 0.75, Source: "test"}}
		st.Settled["codex-run"] = paidrules.Settlement{ID: "codex-run", Agent: "codex", ChargedUSD: 1.5,
			Started: true, CompleteUsage: true, Ended: time.Now()}
		st.Settled["codex-peer"] = paidrules.Settlement{ID: "codex-peer", Agent: "codex", ChargedUSD: 0.5,
			Started: true, Ended: time.Now()}
		// A free Claude run that went on paid overage after the last read.
		st.Settled["claude-overage"] = paidrules.Settlement{ID: "claude-overage", Agent: "claude", ChargedUSD: 0.2,
			Started: true, CompleteUsage: true, Ended: later,
			Evidence: account.RunEvidence{Expected: true, Seen: true, UsingOverage: true}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func joined(findings []Finding) string {
	var lines []string
	for _, f := range findings {
		lines = append(lines, f.Level+": "+f.Message)
	}
	return strings.Join(lines, "\n")
}

// TestDoctorWarnsOfRetainedHolds is re-review finding N2: a hold that
// reconciliation keeps without a conclusion is a warning that names it, its
// reason and the command that ends it.
func TestDoctorWarnsOfRetainedHolds(t *testing.T) {
	cfg, opts, _ := setup(t)
	g := reportGate(t, cfg, shapeReads)
	if err := g.Store.Update(context.Background(), func(st *paidruns.State) error {
		st.Reservations["pane"] = paidrules.Reservation{ID: "pane", Agent: "claude", Shape: corelaunch.Peer, Peer: "p1",
			Session: true, Task: true, Created: time.Now(), Retained: "its supervisor ended during the launch; a child may run unrecorded"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	text := joined(Run(context.Background(), testDeps, cfg, opts))
	if !strings.Contains(text, "warning: held: p1 (claude peer, since ") ||
		!strings.Contains(text, "a child may run unrecorded; end it with duolog peer release p1") {
		t.Fatalf("no warning of the retained hold in\n%s", text)
	}
}

// Doctor reports, per agent and shape, the verdict with its named signals
// and layers, the account kind with its reasons and usable share, a matched
// endpoint entry, the fast pin, withheld names and ignored config, the
// rolling spend with what is in flight and what counts at estimates, the
// tier with its votes and unmeasured rates, and the agent and total session
// counts. It warns of paid overage, says when a free run went on paid
// overage, and compares the lead with the worker. A lead without an
// observation is unknown. No value, URL, environment or settings appears. A
// failed read keeps the route evidence it found and stays paid.
func TestQuotaAndDoctorShapeReports(t *testing.T) {
	t.Run("all shapes", func(t *testing.T) {
		cfg, opts, _ := setup(t)
		t.Setenv("ANTHROPIC_BASE_URL", reportEndpoint)
		t.Setenv("OPENAI_API_KEY", reportKey)
		cfg.EnvPassthrough = append(cfg.EnvPassthrough, "ANTHROPIC_BASE_URL", "OPENAI_API_KEY")
		pc := cfg.PaidRuns.Agent("claude")
		pc.SubscriptionEndpoints = map[string]string{"corp": reportEndpoint}
		pc.Account = "workspace"
		cfg.PaidRuns.Set("claude", pc)
		px := cfg.PaidRuns.Agent("codex")
		px.Allow, px.DailyCapUSD = true, 5
		cfg.PaidRuns.Set("codex", px)
		cfg.Notices = append(cfg.Notices, config.ConfigNotice{Key: "paid_runs.claude.daily_cap_usd", Reason: "the shared project config may only lower it"})
		g := reportGate(t, cfg, shapeReads)
		seed(t, g)
		opts.Lead = map[string]session.LeadRoute{"claude": {Agent: "claude", Argv: []string{"claude", "--model", "opus"},
			Environ: []string{"PATH=/usr/bin", "ANTHROPIC_API_KEY=" + reportLeadKey}}}
		text := joined(Run(context.Background(), testDeps, cfg, opts))
		for _, want := range []string{
			"claude worker: subscription_overage",
			"paid overage is on; duolog stops before it",
			"claude worker ran on paid overage; duolog stopped further runs",
			"account workspace, usable share 0.5 (paid_runs.claude.account)",
			"subscription endpoint: corp",
			"fast mode pinned off",
			"ignored config: paid_runs.claude.daily_cap_usd (the shared project config may only lower it)",
			"claude sessions 1 of 3 (tier small: workspace account",
			"unmeasured",
			"sessions 2 of 10 in all",
			"claude peer: subscription_overage",
			"codex worker: paid: auth:apiKey (account/read)",
			"withheld: OPENAI_API_KEY",
			"paid allowed: 2.00 of 5 USD in 24 h, 0.75 in flight",
			"0.50 of it at estimates: peers report no token counts",
			"codex sessions 1 of 3 (tier small: paid verdict",
			"claude lead: paid: env:ANTHROPIC_API_KEY (env)",
			"claude lead: paid (env:ANTHROPIC_API_KEY); claude worker: subscription_overage (overage:enabled",
			"codex lead: unknown",
			"managed policy outside files is not checked",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("missing %q in\n%s", want, text)
			}
		}
		// Review finding M1: the account-wide lines come once, not per agent.
		for _, once := range []string{"sessions 2 of 10 in all", "ignored config: "} {
			if n := strings.Count(text, once); n != 1 {
				t.Errorf("%q appears %d times, want once:\n%s", once, n, text)
			}
		}
		for _, secret := range []string{reportEndpoint, reportKey, reportLeadKey, "fastMode", "autoMemoryEnabled"} {
			if strings.Contains(text, secret) {
				t.Errorf("report shows %q:\n%s", secret, text)
			}
		}
		// Reporting reserves nothing and writes no state.
		var st paidruns.State
		if err := g.Store.Update(context.Background(), func(s *paidruns.State) error { st = *s; return nil }); err != nil {
			t.Fatal(err)
		}
		if len(st.Reservations) != 2 || len(st.Tickets) != 0 {
			t.Fatalf("reporting changed the reservations: %+v", st.Reservations)
		}
	})

	t.Run("a failed read keeps its evidence", func(t *testing.T) {
		cfg, opts, _ := setup(t)
		reportGate(t, cfg, func(ctx context.Context, p corelaunch.Process) (account.Snapshot, error) {
			s, _ := paidrunstest.Subscription(ctx, p)
			s.Billing.RouteSignals = []corelaunch.Signal{{Name: "auth:apiKey", Layer: "auth status"}}
			return account.Snapshot{Billing: s.Billing}, errors.New("usage read failed")
		})
		text := joined(Run(context.Background(), testDeps, cfg, opts))
		for _, want := range []string{"claude worker: paid: auth:apiKey (auth status), read:failed", "codex worker: paid: auth:apiKey (auth status), read:failed"} {
			if !strings.Contains(text, want) {
				t.Errorf("missing %q in\n%s", want, text)
			}
		}
		if strings.Contains(text, ": subscription") {
			t.Fatalf("a failed read fell back to a cheaper verdict:\n%s", text)
		}
	})
}
