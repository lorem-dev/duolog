package wiring

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/app/paidruns/paidrunstest"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// An observed lead is reported, never run: Build(Lead, Free) keeps every
// byte of its argv and environment, adds no worker variable, settings or
// pin, and the report admits nothing and runs nothing. A lead whose command
// and environment were not observed has no input, so it reports unknown
// rather than the worker's verdict.
func TestLeadBillingIsObservationOnly(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	cfg := &config.Config{EnvPassthrough: []string{"HOME", "PATH"}}
	observed := session.LeadRoute{Agent: "claude",
		Argv:    []string{"claude", "--model", "opus", "--settings", `{"fastMode":true}`},
		Environ: []string{"PATH=/usr/bin", "ANTHROPIC_API_KEY=sk-lead-observed", "DUOLOG_DEPTH=", "ODD= spaced value "}}
	in, ok := session.LeadInput(cfg, observed, home, root)
	if !ok || in.Shape != workerenv.Lead || in.Variant != workerenv.Free || in.Locations.Root != root {
		t.Fatalf("lead input: %+v %v", in, ok)
	}
	p, err := workerenv.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Env, observed.Environ) || !slices.Equal(p.Prefix, observed.Argv) {
		t.Fatalf("observed bytes changed: env %q prefix %q", p.Env, p.Prefix)
	}
	if p.Settings != "" || p.FastPinned || slices.ContainsFunc(p.Env, func(v string) bool { return v == "DUOLOG_DEPTH=1" }) {
		t.Fatalf("worker injections in a lead: %+v", p)
	}
	if !slices.Contains(p.Signals, workerenv.Signal{Name: "env:ANTHROPIC_API_KEY", Layer: "env"}) {
		t.Fatalf("lead signals: %v", p.Signals)
	}
	// The observation itself is never changed.
	if observed.Environ[2] != "DUOLOG_DEPTH=" {
		t.Fatal("the observation was changed")
	}

	for _, missing := range []session.LeadRoute{{Agent: "claude"}, {Agent: "claude", Argv: []string{"claude"}}, {Agent: "rex", Argv: []string{"rex"}, Environ: []string{}}} {
		if _, ok := session.LeadInput(cfg, missing, home, root); ok {
			t.Fatalf("no observation made an input: %+v", missing)
		}
	}

	// The lead's report reads nothing and reserves nothing.
	dir := filepath.Join(t.TempDir(), "accounts")
	g, err := paidrunstest.Gate(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	reads := map[workerenv.Shape]int{}
	g.Cache.Read = func(ctx context.Context, p workerenv.Process) (quota.Snapshot, error) {
		reads[p.Shape]++
		return paidrunstest.Subscription(ctx, p)
	}
	worker := workerenv.ProcessInput{Agent: "claude", Shape: workerenv.Worker, Variant: workerenv.Free, Config: cfg, Environ: os.Environ()}
	if _, err := g.Report(context.Background(), worker); err != nil {
		t.Fatal(err)
	}
	lead, err := g.Report(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if lead.Assessment.Shape != workerenv.Lead || lead.Assessment.Verdict != paidruns.Paid || reads[workerenv.Lead] != 0 || reads[workerenv.Worker] != 1 {
		t.Fatalf("lead report: %+v, reads %v", lead.Assessment, reads)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); !os.IsNotExist(err) {
		t.Fatalf("reporting wrote account state: %v", err)
	}
}
