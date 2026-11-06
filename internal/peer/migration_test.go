package peer

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/delegate"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/paidruns"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/testbin"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// The migration line comes once per agent and lead, whichever of delegate
// and peer open launches that agent first. Delegate claims it with
// delegate.ClaimMigration, the call this test makes for it.
func TestMigrationOnceAcrossDelegateAndPeer(t *testing.T) {
	const secret = "sk-test-value"
	r := newRig(t, withScreens(claudeIdle("")))
	t.Setenv("ANTHROPIC_API_KEY", secret)
	t.Setenv("OPENAI_API_KEY", "")
	if err := os.Unsetenv("OPENAI_API_KEY"); err != nil {
		t.Fatal(err)
	}
	line := workerenv.MigrationLine("claude", "ANTHROPIC_API_KEY")

	// Peer first: the pane opens without the key and the reply names it.
	e := r.open("plan")
	if e.State != Ready || strings.Count(e.Note, line) != 1 || strings.Contains(e.Note, secret) {
		t.Fatalf("%+v", e)
	}
	for _, w := range r.started() {
		if strings.HasPrefix(w, "ANTHROPIC_API_KEY=") || strings.Contains(w, secret) {
			t.Fatal("the pane got the key", w)
		}
	}
	if got, err := delegate.ClaimMigration(r.reg, r.cfg, "lead-1", "claude"); err != nil || len(got) != 0 {
		t.Fatal("delegate repeated the notice", got, err)
	}
	// The live pane is returned, not launched again: no line.
	if e = r.open("plan"); strings.Contains(e.Note, line) {
		t.Fatalf("%+v", e)
	}
	// Codex has no dropped key here.
	if got, err := delegate.ClaimMigration(r.reg, r.cfg, "lead-1", "codex"); err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}

	// Delegate first, in a new lead: the peer then adds nothing.
	if err := r.reg.RegisterLead(session.Lead{ID: "lead-2", Target: config.Target{Agent: "codex", Model: "sol", Effort: "high"}, Mode: mode.Ask, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	if got, err := delegate.ClaimMigration(r.reg, r.cfg, "lead-2", "claude"); err != nil || len(got) != 1 || got[0] != line {
		t.Fatal(got, err)
	}
	r.ctx = WithOptions(context.Background(), Options{Root: r.root, Config: r.cfg, Lead: "lead-2"})
	if e = r.open("plan"); e.State != Ready || strings.Contains(e.Note, line) {
		t.Fatalf("%+v", e)
	}
}

// The claimed line comes whole after a question, which is cut before it, and
// after an error, since peer open claims it before the pane exists.
func TestMigrationSurvivesQuestionAndError(t *testing.T) {
	line := workerenv.MigrationLine("codex", "OPENAI_API_KEY")
	keys := func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "sk-test-value")
		t.Setenv("ANTHROPIC_API_KEY", "")
		if err := os.Unsetenv("ANTHROPIC_API_KEY"); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("question", func(t *testing.T) {
		r := newRig(t, withScreens(fixtureText("codex-trust-live.txt")))
		keys(t)
		e := r.open("implement")
		if e.State != Question || !strings.Contains(e.Question, "Trust this folder?") || strings.Count(e.Question, line) != 1 || !strings.HasSuffix(e.Question, "; "+line) {
			t.Fatalf("%+v", e)
		}
	})

	t.Run("error", func(t *testing.T) {
		script := baseScript()
		script["new"] = testbin.Scenario{Exit: 4, Stderr: "connecting to Rex server\nfailed to WebSocket dial\n"}
		r := newRig(t, script)
		keys(t)
		_, err := Open(r.ctx, "implement")
		if err == nil || strings.Count(err.Error(), line) != 1 || !strings.HasSuffix(err.Error(), "; "+line) {
			t.Fatalf("%v", err)
		}
		// The notice is claimed: the next open in this lead adds nothing.
		r.rex(baseScript())
		if e := r.open("implement"); e.State != Ready || strings.Contains(e.Note, "no longer get") {
			t.Fatalf("%+v", e)
		}
	})

	// A pane the account gate refuses opens nothing, and the refusal still
	// carries the claimed line.
	t.Run("refused", func(t *testing.T) {
		r := newRig(t, baseScript())
		keys(t)
		read := r.gate.Cache.Read
		paid := true
		r.gate.Cache.Read = func(ctx context.Context, p workerenv.Process) (quota.Snapshot, error) {
			s, err := read(ctx, p)
			if paid {
				s.Billing.RouteSignals = []workerenv.Signal{{Name: "auth:apiKey", Layer: "account/read"}}
			}
			return s, err
		}
		_, err := Open(r.ctx, "implement")
		if err == nil || !strings.Contains(err.Error(), "codex peer is paid") || strings.Count(err.Error(), line) != 1 || !strings.HasSuffix(err.Error(), "; "+line) {
			t.Fatalf("%v", err)
		}
		if len(r.spawns()) != 0 {
			t.Fatal("a refused pane was opened")
		}
		// A fresh read, not the cached paid one.
		paid = false
		r.gate.Cache = &paidruns.Cache{Read: read}
		if e := r.open("implement"); e.State != Ready || strings.Contains(e.Note, "no longer get") {
			t.Fatalf("%+v", e)
		}
	})
}
