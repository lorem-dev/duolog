package paidrules

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	coreaccount "github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/launch"
)

const secretValue = "sk-test-secret-value"

var now = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func yes() *bool { v := true; return &v }
func no() *bool  { v := false; return &v }

func testConfig() *config.Config {
	cfg := &config.Config{}
	cfg.PaidRuns.UsableShare = 0.5
	for _, name := range []string{"claude", "codex"} {
		cfg.PaidRuns.Set(name, config.PaidAgent{Account: "auto"})
	}
	return cfg
}

func windows() []coreaccount.Window {
	return []coreaccount.Window{
		{Kind: "session", DurationMinutes: 300, UsedPercent: 12, ResetsAt: now.Add(2 * time.Hour)},
		{Kind: "weekly_all", DurationMinutes: 10080, UsedPercent: 30, ResetsAt: now.Add(72 * time.Hour)},
	}
}

// snapshot is a successful first-party read with future windows and no
// overage.
func snapshot(agent string) coreaccount.Snapshot {
	s := coreaccount.Snapshot{Plan: "max", Windows: windows(), ReadAt: now.Add(-10 * time.Second)}
	switch agent {
	case "claude":
		s.Billing = coreaccount.BillingFacts{AuthMethod: "claude.ai", APIProvider: "firstParty", RouteKnown: true, WindowsAvailable: yes()}
	case "codex":
		s.Plan = "plus"
		s.Billing = coreaccount.BillingFacts{AccountType: "chatgpt", AuthMode: "chatgpt", RouteKnown: true, OrdinaryUsageAllowed: yes()}
	}
	return s
}

func process(agent string, shape launch.Shape, variant launch.Variant, signals ...launch.Signal) launch.Process {
	return launch.Process{Agent: agent, Shape: shape, Variant: variant, Signals: signals,
		Env: []string{"SOME_KEY=" + secretValue}, Settings: `{"env":{"X":"` + secretValue + `"}}`}
}

func hasSignal(a Assessment, name string) bool {
	return slices.ContainsFunc(a.Signals, func(s launch.Signal) bool { return s.Name == name })
}

func signalNames(a Assessment) []string {
	var names []string
	for _, s := range a.Signals {
		names = append(names, s.Name+"@"+s.Layer)
	}
	return names
}

func TestVerdictRouteWinsAcrossShapes(t *testing.T) {
	type route struct {
		name    string
		signals []launch.Signal
		billing func(*coreaccount.BillingFacts)
		want    string
	}
	routes := map[string][]route{
		"claude": {
			{name: "env key", signals: []launch.Signal{{Name: "env:ANTHROPIC_API_KEY", Layer: "env"}}, want: "env:ANTHROPIC_API_KEY"},
			{name: "settings env", signals: []launch.Signal{{Name: "env:ANTHROPIC_BASE_URL", Layer: "user"}}, want: "env:ANTHROPIC_BASE_URL"},
			{name: "helper", signals: []launch.Signal{{Name: "apiKeyHelper", Layer: "project"}}, want: "apiKeyHelper"},
			{name: "managed fast", signals: []launch.Signal{{Name: "fastMode", Layer: "managed"}}, want: "fastMode"},
			{name: "unreadable", signals: []launch.Signal{{Name: "unreadable", Layer: "local"}}, want: "unreadable"},
			{name: "auth api key", billing: func(b *coreaccount.BillingFacts) {
				b.AuthMethod = "api_key"
				b.RouteSignals = []launch.Signal{{Name: "auth:api_key", Layer: "auth status"}}
			}, want: "auth:api_key"},
			{name: "auth unread", billing: func(b *coreaccount.BillingFacts) {
				b.AuthMethod, b.APIProvider, b.RouteKnown = "", "", false
				b.RouteSignals = []launch.Signal{{Name: "auth:unread", Layer: "auth status"}}
			}, want: "auth:unread"},
			{name: "cloud provider", billing: func(b *coreaccount.BillingFacts) {
				b.APIProvider = "bedrock"
				b.RouteSignals = []launch.Signal{{Name: "auth:apiProvider", Layer: "auth status"}}
			}, want: "auth:apiProvider"},
			{name: "unknown provider", billing: func(b *coreaccount.BillingFacts) {
				b.APIProvider, b.RouteKnown = "unknown", false
				b.RouteSignals = []launch.Signal{{Name: "auth:apiProvider", Layer: "auth status"}}
			}, want: "auth:apiProvider"},
		},
		"codex": {
			{name: "env key", signals: []launch.Signal{{Name: "env:OPENAI_API_KEY", Layer: "env"}}, want: "env:OPENAI_API_KEY"},
			{name: "provider in settings", signals: []launch.Signal{{Name: "model_provider", Layer: "user"}}, want: "model_provider"},
			{name: "speed", signals: []launch.Signal{{Name: "route:unknown-speed", Layer: "argv"}}, want: "route:unknown-speed"},
			{name: "unreadable", signals: []launch.Signal{{Name: "unreadable", Layer: "project"}}, want: "unreadable"},
			{name: "auth headers", billing: func(b *coreaccount.BillingFacts) {
				b.AuthMode = "headers"
				b.RouteSignals = []launch.Signal{{Name: "auth:headers", Layer: "account/read"}}
			}, want: "auth:headers"},
			{name: "api key account", billing: func(b *coreaccount.BillingFacts) {
				b.AccountType = "apiKey"
				b.RouteSignals = []launch.Signal{{Name: "account:apiKey", Layer: "account/read"}}
			}, want: "account:apiKey"},
			{name: "unknown provider", billing: func(b *coreaccount.BillingFacts) {
				b.RouteKnown = false
				b.RouteSignals = []launch.Signal{{Name: "provider:unknown", Layer: "config/read"}}
			}, want: "provider:unknown"},
			{name: "provider base url", billing: func(b *coreaccount.BillingFacts) {
				b.RouteSignals = []launch.Signal{{Name: "provider:base_url", Layer: "config/read"}}
			}, want: "provider:base_url"},
		},
	}
	cfg := testConfig()
	for _, agent := range []string{"claude", "codex"} {
		for _, shape := range []launch.Shape{launch.Worker, launch.Peer, launch.Lead} {
			for _, variant := range []launch.Variant{launch.Free, launch.Paid} {
				prefix := agent + "/" + string(shape) + "/" + string(variant)
				t.Run(prefix+"/first party", func(t *testing.T) {
					a := Evaluate(process(agent, shape, variant), snapshot(agent), nil, coreaccount.RunEvidence{}, cfg, now)
					if a.Verdict != Subscription || len(a.Signals) != 0 {
						t.Fatalf("verdict = %s %v, want subscription without signals", a.Verdict, signalNames(a))
					}
					if a.Agent != agent || a.Shape != shape || a.Variant != variant {
						t.Fatalf("key = %s/%s/%s", a.Agent, a.Shape, a.Variant)
					}
				})
				t.Run(prefix+"/overage on", func(t *testing.T) {
					s := snapshot(agent)
					s.Billing.OverageEnabled = true
					a := Evaluate(process(agent, shape, variant), s, nil, coreaccount.RunEvidence{}, cfg, now)
					if a.Verdict != SubscriptionOverage || !hasSignal(a, "overage:enabled") {
						t.Fatalf("verdict = %s %v, want subscription_overage with overage:enabled", a.Verdict, signalNames(a))
					}
				})
				for _, r := range routes[agent] {
					for _, overage := range []bool{false, true} {
						t.Run(prefix+"/"+r.name, func(t *testing.T) {
							s := snapshot(agent)
							s.Billing.OverageEnabled = overage
							if r.billing != nil {
								r.billing(&s.Billing)
							}
							a := Evaluate(process(agent, shape, variant, r.signals...), s, nil, coreaccount.RunEvidence{}, cfg, now)
							if a.Verdict != Paid || !hasSignal(a, r.want) {
								t.Fatalf("verdict = %s %v, want paid with %s despite a future window", a.Verdict, signalNames(a), r.want)
							}
							if hasSignal(a, "overage:enabled") {
								t.Fatalf("paid verdict names the overage signal: %v", signalNames(a))
							}
						})
					}
				}
			}
		}
		t.Run(agent+"/plan names", func(t *testing.T) {
			base := Evaluate(process(agent, launch.Worker, launch.Free), snapshot(agent), nil, coreaccount.RunEvidence{}, cfg, now)
			paid := snapshot(agent)
			paid.Billing.RouteSignals = []launch.Signal{{Name: "auth:unknown", Layer: "auth status"}}
			paid.Billing.RouteKnown = false
			paidBase := Evaluate(process(agent, launch.Worker, launch.Free), paid, nil, coreaccount.RunEvidence{}, cfg, now)
			for _, plan := range []string{"", "free", "pro", "max", "plus", "team", "business", "enterprise", "edu"} {
				s := snapshot(agent)
				s.Plan = plan
				if a := Evaluate(process(agent, launch.Worker, launch.Free), s, nil, coreaccount.RunEvidence{}, cfg, now); a.Verdict != base.Verdict || a.Account != base.Account {
					t.Fatalf("plan %q: %s %s, want %s %s", plan, a.Verdict, a.Account, base.Verdict, base.Account)
				}
				paid.Plan = plan
				if a := Evaluate(process(agent, launch.Worker, launch.Free), paid, nil, coreaccount.RunEvidence{}, cfg, now); a.Verdict != paidBase.Verdict {
					t.Fatalf("plan %q turned a paid route into %s", plan, a.Verdict)
				}
			}
		})
	}
	t.Run("names only", func(t *testing.T) {
		p := process("claude", launch.Worker, launch.Paid, launch.Signal{Name: "env:ANTHROPIC_API_KEY", Layer: "env"})
		data, err := json.Marshal(Evaluate(p, snapshot("claude"), errors.New("read "+secretValue), coreaccount.RunEvidence{}, cfg, now))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), secretValue) {
			t.Fatalf("assessment carries a value: %s", data)
		}
	})
}

func TestVerdictMissingStaleAndRunEvidence(t *testing.T) {
	cfg := testConfig()
	eval := func(agent string, s coreaccount.Snapshot, readErr error, e coreaccount.RunEvidence) Assessment {
		return Evaluate(process(agent, launch.Worker, launch.Free), s, readErr, e, cfg, now)
	}
	wantPaid := func(t *testing.T, a Assessment, signal string) {
		t.Helper()
		if a.Verdict != Paid || !hasSignal(a, signal) {
			t.Fatalf("verdict = %s %v, want paid with %s", a.Verdict, signalNames(a), signal)
		}
	}
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent+"/failed read keeps paid evidence", func(t *testing.T) {
			s := coreaccount.Snapshot{Billing: snapshot(agent).Billing}
			wantPaid(t, eval(agent, s, errors.New("usage read failed"), coreaccount.RunEvidence{}), "read:failed")
			// A failed read with windows left over still decides nothing.
			wantPaid(t, eval(agent, snapshot(agent), errors.New("timed out"), coreaccount.RunEvidence{}), "read:failed")
			s.Billing.RouteSignals = []launch.Signal{{Name: "auth:api_key", Layer: "auth status"}}
			a := eval(agent, s, errors.New("usage read failed"), coreaccount.RunEvidence{})
			if !hasSignal(a, "read:failed") || !hasSignal(a, "auth:api_key") {
				t.Fatalf("signals = %v, want the failure and the route evidence", signalNames(a))
			}
		})
		t.Run(agent+"/absent read", func(t *testing.T) {
			a := eval(agent, coreaccount.Snapshot{}, nil, coreaccount.RunEvidence{})
			wantPaid(t, a, "read:stale")
			if !hasSignal(a, "read:no-window") || !hasSignal(a, "route:unknown") {
				t.Fatalf("signals = %v, want no-window and route:unknown", signalNames(a))
			}
		})
		t.Run(agent+"/stale read", func(t *testing.T) {
			s := snapshot(agent)
			s.ReadAt = now.Add(-120 * time.Second)
			if a := eval(agent, s, nil, coreaccount.RunEvidence{}); a.Verdict != Subscription {
				t.Fatalf("read at the max age: %s %v", a.Verdict, signalNames(a))
			}
			s.ReadAt = now.Add(-121 * time.Second)
			wantPaid(t, eval(agent, s, nil, coreaccount.RunEvidence{}), "read:stale")
		})
		t.Run(agent+"/no future window", func(t *testing.T) {
			s := snapshot(agent)
			s.Windows = nil
			wantPaid(t, eval(agent, s, nil, coreaccount.RunEvidence{}), "read:no-window")
			s.Windows = windows()
			for i := range s.Windows {
				s.Windows[i].ResetsAt = now.Add(-time.Minute)
			}
			wantPaid(t, eval(agent, s, nil, coreaccount.RunEvidence{}), "read:no-window")
			for i := range s.Windows {
				s.Windows[i].ResetsAt = time.Time{}
			}
			wantPaid(t, eval(agent, s, nil, coreaccount.RunEvidence{}), "read:no-window")
			// A model family's window alone is no account window.
			s.Windows = nil
			s.ModelLimits = map[string][]coreaccount.Window{"sonnet": windows()}
			wantPaid(t, eval(agent, s, nil, coreaccount.RunEvidence{}), "read:no-window")
		})
		t.Run(agent+"/expected run evidence missing", func(t *testing.T) {
			e := coreaccount.RunEvidence{Expected: true}
			wantPaid(t, eval(agent, snapshot(agent), nil, e), "run:no-evidence")
			if !e.Charged() {
				t.Fatal("a run without its expected evidence is not charged as paid")
			}
			e.Seen = true
			if a := eval(agent, snapshot(agent), nil, e); a.Verdict != Subscription || e.Charged() {
				t.Fatalf("seen evidence: %s %v, charged %t", a.Verdict, signalNames(a), e.Charged())
			}
		})
		t.Run(agent+"/overage used", func(t *testing.T) {
			e := coreaccount.RunEvidence{Expected: true, Seen: true, UsingOverage: true}
			if !e.Charged() {
				t.Fatal("an attempt on overage is not charged as paid")
			}
			a := eval(agent, snapshot(agent), nil, e)
			if a.Verdict != SubscriptionOverage || !hasSignal(a, "run:overage") {
				t.Fatalf("next admission: %s %v, want subscription_overage with run:overage", a.Verdict, signalNames(a))
			}
			// A later benign event of the same run never clears it.
			e.Add(coreaccount.RunEvidence{Expected: true, Seen: true})
			if !e.UsingOverage || !e.Charged() {
				t.Fatalf("benign event cleared the overage: %+v", e)
			}
			if a := eval(agent, snapshot(agent), nil, e); a.Verdict != SubscriptionOverage {
				t.Fatalf("after a benign event: %s", a.Verdict)
			}
			// Overage at best: other paid evidence still wins.
			s := snapshot(agent)
			s.Windows = nil
			wantPaid(t, eval(agent, s, nil, e), "read:no-window")
			// A new successful read comes without the old run's evidence.
			if a := eval(agent, snapshot(agent), nil, coreaccount.RunEvidence{}); a.Verdict != Subscription {
				t.Fatalf("after a new read: %s", a.Verdict)
			}
		})
		t.Run(agent+"/overage enabled in the run", func(t *testing.T) {
			e := coreaccount.RunEvidence{Expected: true, Seen: true, OverageEnabled: true}
			a := eval(agent, snapshot(agent), nil, e)
			if a.Verdict != SubscriptionOverage || !hasSignal(a, "overage:enabled") || e.Charged() {
				t.Fatalf("verdict = %s %v, charged %t", a.Verdict, signalNames(a), e.Charged())
			}
		})
		t.Run(agent+"/fast run", func(t *testing.T) {
			e := coreaccount.RunEvidence{Expected: true, Seen: true, Fast: true}
			if !e.Charged() {
				t.Fatal("a fast run is not charged as paid")
			}
			wantPaid(t, eval(agent, snapshot(agent), nil, e), "run:fastMode")
		})
		t.Run(agent+"/fast mode in the person's settings", func(t *testing.T) {
			// Ruling 2: an effective fastMode true is a paid signal in the
			// paid variant and the observed lead.
			for _, p := range []launch.Process{
				process(agent, launch.Worker, launch.Paid, launch.Signal{Name: "fastMode", Layer: "user"}),
				process(agent, launch.Lead, launch.Free, launch.Signal{Name: "fastMode", Layer: "project"}),
			} {
				wantPaid(t, Evaluate(p, snapshot(agent), nil, coreaccount.RunEvidence{}, cfg, now), "fastMode")
			}
		})
	}
	t.Run("claude windows unavailable", func(t *testing.T) {
		s := snapshot("claude")
		s.Billing.WindowsAvailable = no()
		wantPaid(t, eval("claude", s, nil, coreaccount.RunEvidence{}), "read:windows-unavailable")
		s.Billing.WindowsAvailable = nil
		if a := eval("claude", s, nil, coreaccount.RunEvidence{}); a.Verdict != Subscription {
			t.Fatalf("no availability flag with windows: %s %v", a.Verdict, signalNames(a))
		}
	})
	t.Run("codex ordinary usage", func(t *testing.T) {
		s := snapshot("codex")
		for _, allowed := range []*bool{nil, no()} {
			s.Billing.OrdinaryUsageAllowed = allowed
			wantPaid(t, eval("codex", s, nil, coreaccount.RunEvidence{}), "read:ordinary-usage")
		}
	})
	t.Run("scoped rejection", func(t *testing.T) {
		s := snapshot("codex")
		rejected := windows()
		for i := range rejected {
			rejected[i].Severity = "rejected"
		}
		s.ModelLimits = map[string][]coreaccount.Window{"sol": rejected, "terra": windows()}
		if a := eval("codex", s, nil, coreaccount.RunEvidence{}); a.Verdict != Subscription {
			t.Fatalf("a family's rejection changed the agent's verdict: %s %v", a.Verdict, signalNames(a))
		}
		if !FamilyBlocked(s, "sol") || FamilyBlocked(s, "terra") || FamilyBlocked(s, "") {
			t.Fatal("a scoped rejection must block its family only")
		}
		s.Rejected = true
		if !FamilyBlocked(s, "terra") || !FamilyBlocked(s, "") {
			t.Fatal("an account rejection must block every family")
		}
	})
	t.Run("unknown agent", func(t *testing.T) {
		wantPaid(t, eval("other", snapshot("claude"), nil, coreaccount.RunEvidence{}), "agent:unknown")
	})
}

func TestAccountEvidenceAndUsableShare(t *testing.T) {
	wantAccount := func(t *testing.T, a Assessment, account string, share float64, reasons ...string) {
		t.Helper()
		if a.Account != account || a.UsableShare != share || !slices.Equal(a.AccountReasons, reasons) {
			t.Fatalf("account = %s u=%v %v, want %s u=%v %v", a.Account, a.UsableShare, a.AccountReasons, account, share, reasons)
		}
	}
	eval := func(agent string, s coreaccount.Snapshot, e coreaccount.RunEvidence, cfg *config.Config) Assessment {
		return Evaluate(process(agent, launch.Worker, launch.Free), s, nil, e, cfg, now)
	}
	reasons := map[string][]string{
		"claude": {"org", "seat_tier", "group", "member"},
		"codex": {"workspace_name", "workspace_plan", "forced_workspace", "spend_control", "workspace_owner_credits_depleted", "workspace_member_credits_depleted",
			"workspace_owner_usage_limit_reached", "workspace_member_usage_limit_reached"},
	}
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent+"/auto without evidence", func(t *testing.T) {
			for _, plan := range []string{"", "pro", "max", "plus", "team", "business", "enterprise", "edu"} {
				s := snapshot(agent)
				s.Plan = plan
				wantAccount(t, eval(agent, s, coreaccount.RunEvidence{}, testConfig()), "personal", 1)
			}
			// A config without an account kind is auto.
			cfg := testConfig()
			cfg.PaidRuns.Set("claude", config.PaidAgent{})
			cfg.PaidRuns.Set("codex", config.PaidAgent{})
			wantAccount(t, eval(agent, snapshot(agent), coreaccount.RunEvidence{}, cfg), "personal", 1)
		})
		for _, reason := range reasons[agent] {
			t.Run(agent+"/read reason "+reason, func(t *testing.T) {
				s := snapshot(agent)
				s.Billing.WorkspaceReasons = []string{reason}
				a := eval(agent, s, coreaccount.RunEvidence{}, testConfig())
				wantAccount(t, a, "workspace", 0.5, reason)
				if a.Verdict != Subscription {
					t.Fatalf("workspace evidence changed the verdict: %s", a.Verdict)
				}
				cfg := testConfig()
				cfg.PaidRuns.UsableShare = 0.25
				wantAccount(t, eval(agent, s, coreaccount.RunEvidence{}, cfg), "workspace", 0.25, reason)
			})
		}
		t.Run(agent+"/run reason", func(t *testing.T) {
			e := coreaccount.RunEvidence{Expected: true, Seen: true, WorkspaceReasons: []string{reasons[agent][0]}}
			wantAccount(t, eval(agent, snapshot(agent), e, testConfig()), "workspace", 0.5, reasons[agent][0])
			// Only safe categories count; anything else is dropped unseen.
			e.WorkspaceReasons = []string{"Example Team " + secretValue}
			a := eval(agent, snapshot(agent), e, testConfig())
			wantAccount(t, a, "personal", 1)
			data, _ := json.Marshal(a)
			if strings.Contains(string(data), secretValue) {
				t.Fatalf("assessment carries a raw reason: %s", data)
			}
		})
		t.Run(agent+"/forced workspace", func(t *testing.T) {
			cfg := testConfig()
			cfg.PaidRuns.Set(agent, config.PaidAgent{Account: "workspace"})
			key := "paid_runs." + agent + ".account"
			wantAccount(t, eval(agent, snapshot(agent), coreaccount.RunEvidence{}, cfg), "workspace", 0.5, key)
			cfg.PaidRuns.UsableShare = 0.3
			s := snapshot(agent)
			s.Billing.WorkspaceReasons = []string{reasons[agent][0]}
			wantAccount(t, eval(agent, s, coreaccount.RunEvidence{}, cfg), "workspace", 0.3, reasons[agent][0], key)
		})
		t.Run(agent+"/personal with workspace evidence", func(t *testing.T) {
			// Ruling 1: explicit personal never suppresses positive
			// workspace evidence; it means personal only without evidence.
			cfg := testConfig()
			cfg.PaidRuns.Set("claude", config.PaidAgent{Account: "personal"})
			cfg.PaidRuns.Set("codex", config.PaidAgent{Account: "personal"})
			wantAccount(t, eval(agent, snapshot(agent), coreaccount.RunEvidence{}, cfg), "personal", 1)
			s := snapshot(agent)
			s.Billing.WorkspaceReasons = []string{reasons[agent][0]}
			wantAccount(t, eval(agent, s, coreaccount.RunEvidence{}, cfg), "workspace", 0.5, reasons[agent][0])
		})
		t.Run(agent+"/evidence kept on failure", func(t *testing.T) {
			s := coreaccount.Snapshot{Billing: snapshot(agent).Billing}
			s.Billing.WorkspaceReasons = []string{reasons[agent][0]}
			a := Evaluate(process(agent, launch.Worker, launch.Free), s, errors.New("failed"), coreaccount.RunEvidence{}, testConfig(), now)
			if a.Verdict != Paid {
				t.Fatalf("failed read: %s", a.Verdict)
			}
			wantAccount(t, a, "workspace", 0.5, reasons[agent][0])
		})
	}
}
