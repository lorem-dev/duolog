package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/app/paidruns/paidrunstest"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/testbin"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// skillGate makes delegateDeps.PaidGate open one gate on a temporary account
// state, whose Claude reads show an API key route when paid is set, and
// returns a pointer to the gate it opened.
func skillGate(t *testing.T, paid bool) **paidruns.Gate {
	t.Helper()
	dir := t.TempDir()
	var g *paidruns.Gate
	testDeps(t).Delegate.PaidGate = func(cfg *config.Config) (*paidruns.Gate, error) {
		var err error
		if g, err = paidrunstest.Gate(dir, cfg); err != nil {
			return nil, err
		}
		g.Cache.Read = func(ctx context.Context, p workerenv.Process) (quota.Snapshot, error) {
			s, err := paidrunstest.Subscription(ctx, p)
			if paid {
				s.Billing.RouteSignals = []workerenv.Signal{{Name: "auth:apiKey", Layer: "auth status"}}
			}
			return s, err
		}
		return g, nil
	}
	return &g
}

// duolog skill test admits each target through the account gate as a free
// worker: a free worker whose route is paid is refused before any model
// call, and an admitted run holds a slot and settles once.
func TestSkillTestRunsBehindTheGate(t *testing.T) {
	t.Run("paid route refused", func(t *testing.T) {
		skillProject(t)
		skillGate(t, true)
		fake := testbin.Install(t, "claude", testbin.Scenario{Stdout: `{"result":"{\"s1\":\"YES\"}","usage":{"input_tokens":100,"output_tokens":10}}`})
		out, err := executeBusCommand(t, "skill", "test", "skills/demo/SKILL.md", "--models", "claude/haiku/low", "--yes")
		if err == nil || !strings.Contains(out+fmt.Sprint(err), "claude worker is paid") {
			t.Fatalf("a paid free worker was not refused: %s %v", out, err)
		}
		if _, statErr := os.Stat(fake + ".args.json"); !os.IsNotExist(statErr) {
			t.Fatal("the refused skill test called the model")
		}
	})
	t.Run("subscription run settles once", func(t *testing.T) {
		skillProject(t)
		g := skillGate(t, false)
		testbin.Install(t, "claude", testbin.Scenario{Stdout: `{"result":"{\"s1\":\"YES\"}","usage":{"input_tokens":100,"output_tokens":10}}`})
		out, err := executeBusCommand(t, "skill", "test", "skills/demo/SKILL.md", "--models", "claude/haiku/low", "--yes")
		if err != nil || !strings.Contains(out, "1/1") {
			t.Fatalf("%s %v", out, err)
		}
		if *g == nil {
			t.Fatal("the skill test opened no gate")
		}
		var st paidruns.State
		if err := (*g).Store.View(context.Background(), func(s paidruns.State) error { st = s; return nil }); err != nil {
			t.Fatal(err)
		}
		if len(st.Reservations) != 0 || len(st.Settled) != 1 {
			t.Fatalf("held %+v, settled %+v; want one settlement", st.Reservations, st.Settled)
		}
		for _, s := range st.Settled {
			if !s.Started || s.Agent != "claude" {
				t.Fatalf("settlement %+v", s)
			}
		}
	})
}
