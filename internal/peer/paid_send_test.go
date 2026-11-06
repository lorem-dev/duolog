package peer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/paidruns"
	"github.com/lorem-dev/duolog/internal/paidruns/paidrunstest"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/testbin"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// An open that fails after the launch began, in a way that does not show
// whether rex made the pane, gives its slot and estimate back when the
// pane's env file is still there to take: without it the launcher starts
// nothing. A taken env file means the agent may run, so the hold stays.
func TestPeerOpenFailedMidLaunchFreesProvenUnstartedHold(t *testing.T) {
	for _, c := range []struct {
		name  string
		taken bool
	}{{"env file never read", false}, {"env file taken", true}} {
		t.Run(c.name, func(t *testing.T) {
			script := baseScript()
			// A reply that does not decode: the pane may or may not exist.
			script["new"] = testbin.Scenario{Stdout: "{not json"}
			r := newRig(t, script)
			r.paidCodex()
			if c.taken {
				old := openedEnvFile
				openedEnvFile = func(path string) { _ = os.Remove(path) }
				t.Cleanup(func() { openedEnvFile = old })
			}
			if _, err := Open(r.ctx, "implement"); err == nil {
				t.Fatal("open succeeded")
			}
			held := r.accounts().Reservations
			if !c.taken && len(held) != 0 {
				t.Fatalf("a pane proven never started kept its hold: %+v", held)
			}
			if c.taken && len(held) != 1 {
				t.Fatalf("a pane that may run lost its hold: %+v", held)
			}
			if files, _ := filepath.Glob(filepath.Join(r.root, ".duolog", ".peer-env-*")); len(files) != 0 {
				t.Fatalf("env files left: %v", files)
			}
		})
	}
}

// While rex opens the pane, its hold names the env file as its launch token,
// so a peer open that dies there leaves reconciliation the proof it needs.
func TestPeerOpenRecordsLaunchToken(t *testing.T) {
	r := newRig(t, baseScript())
	var token, file string
	old := openedEnvFile
	openedEnvFile = func(path string) {
		file = path
		for _, res := range r.accounts().Reservations {
			token = res.LaunchToken
		}
	}
	t.Cleanup(func() { openedEnvFile = old })
	r.ready()
	if file == "" || token != file {
		t.Fatalf("launch token %q, env file %q", token, file)
	}
}

// Only the question itself decides whether it chooses billing: pane output
// above it that mentions an API key leaves the configured answers on, and a
// question about an API key still goes to the lead.
func TestBillingQuestionReadsTheQuestionOnly(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.ready()
	dialog := "• I read the docs on the API key setup in README.md.\n\n─────\n\nWhich environment should I use?\n› 1. Staging\n  2. Production\n"
	r.cfg.Rex.AutoAnswers = []config.AutoAnswer{{Match: "Which environment", Answer: "Staging"}}
	r.rex(withScreens(working(), dialog, codexIdle(""), codexIdle("")))
	got, err := Wait(r.ctx, e.ID)
	if err != nil || got.State != Ready || len(r.messages("ask")) != 0 {
		t.Fatalf("a question below output that names an API key was not answered: %+v %v", got, err)
	}
	yes, no := []Option{{Label: "Yes"}}, []Option{{Label: "No"}}
	for _, c := range []struct {
		name    string
		screen  Screen
		billing bool
	}{
		{"API key question", Screen{Question: "Detected a custom API key in your environment Do you want to use this API key?"}, true},
		{"API key option", Screen{Question: "Do you want to use it?", Options: []Option{{Label: "Use the API key"}}}, true},
		{"gateway only", Screen{Question: "Send requests through the company gateway?", Options: append(yes, no...)}, true},
		{"extra usage", Screen{Question: "You reached your limit. Turn on extra usage to keep working?", Options: append(yes, no...)}, true},
		{"overage option", Screen{Question: "How should I go on?", Options: []Option{{Label: "Continue with overage"}, {Label: "Stop"}}}, true},
		{"credits", Screen{Question: "Use your Credits for this session?", Options: yes}, true},
		{"purchase", Screen{Question: "What now?", Options: []Option{{Label: "Purchase more usage"}}}, true},
		{"payment", Screen{Question: "Add a payment method now?", Options: yes}, true},
		{"billing", Screen{Question: "Switch the BILLING mode?", Options: yes}, true},
		{"usage-based", Screen{Question: "Move to usage-based pricing?", Options: yes}, true},
		{"pay as you go", Screen{Question: "Keep going on pay-as-you-go?", Options: yes}, true},
		{"ordinary", Screen{Question: "Which environment should I use?", Options: []Option{{Label: "Staging"}}}, false},
		{"billing only above the question", Screen{Question: "• I read the billing notes. Which environment should I use?", Options: []Option{{Label: "Staging"}}}, false},
	} {
		if got := billingQuestion(c.screen); got != c.billing {
			t.Errorf("%s: billing %v, want %v", c.name, got, c.billing)
		}
	}
}

// The first send takes over its pane's held task only through a fresh
// admission: a cap lowered below the held estimate, or a window that filled
// since the open, refuses it, nothing is typed, and the open keeps its hold.
// An admitted first send replaces the open's hold, never counted twice.
func TestPeerFirstSendIsAdmittedAfresh(t *testing.T) {
	t.Run("cap lowered below the held estimate", func(t *testing.T) {
		r := newRig(t, baseScript())
		r.paidCodex()
		e := r.ready()
		open := r.accounts().Reservations[r.entry(e.ID).AccountSessionID]
		r.cfg.PaidRuns.Codex.DailyCapUSD = open.Estimate.USD / 2
		path := r.task()
		script := baseScript()
		script["capture"] = screens(sendOK(path)...)
		r.rex(script)
		err := Send(r.ctx, e.ID, path)
		if err == nil || !strings.Contains(err.Error(), "daily cap") {
			t.Fatalf("a first send above the cap: %v", err)
		}
		if len(r.named("send")) != 0 {
			t.Fatalf("a refused send typed: %v", r.named("send"))
		}
		if held := r.accounts().Reservations[open.ID]; !held.Task || held.Estimate.USD != open.Estimate.USD {
			t.Fatalf("the open lost its hold: %+v", held)
		}
	})

	t.Run("window filled since the open", func(t *testing.T) {
		r := newRig(t, baseScript())
		used := 10.0
		r.gate.Cache.Read = func(ctx context.Context, p workerenv.Process) (quota.Snapshot, error) {
			s, err := paidrunstest.Subscription(ctx, p)
			s.Windows[0].UsedPercent = used
			return s, err
		}
		e := r.ready()
		used = 95
		r.gate.Cache = &paidruns.Cache{Read: r.gate.Cache.Read}
		path := r.task()
		script := baseScript()
		script["capture"] = screens(sendOK(path)...)
		r.rex(script)
		err := Send(r.ctx, e.ID, path)
		if err == nil || !strings.Contains(err.Error(), "no window headroom") {
			t.Fatalf("a first send past the window reserve: %v", err)
		}
		r.wantHeld(1, 1)
	})

	t.Run("admitted once, not twice", func(t *testing.T) {
		r := newRig(t, baseScript())
		r.paidCodex()
		e := r.ready()
		open := r.accounts().Reservations[r.entry(e.ID).AccountSessionID]
		// The cap holds the one task, not two.
		r.cfg.PaidRuns.Codex.DailyCapUSD = open.Estimate.USD * 1.5
		r.send(e.ID, r.task())
		r.wantHeld(1, 1)
		task := r.accounts().Reservations[r.entry(e.ID).TaskGeneration]
		if !task.Paid || task.Estimate.USD <= 0 || task.SessionID != open.ID {
			t.Fatalf("first send: %+v", task)
		}
	})
}
