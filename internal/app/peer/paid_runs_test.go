package peer

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// accounts is the account state behind the rig's gate.
func (r *rig) accounts() paidruns.State {
	r.t.Helper()
	var st paidruns.State
	if err := r.gate.Store.Update(context.Background(), func(s *paidruns.State) error { st = *s; return nil }); err != nil {
		r.t.Fatal(err)
	}
	return st
}

// held counts the held session and task reservations.
func (r *rig) held() (sessions, tasks int) {
	for _, res := range r.accounts().Reservations {
		if res.Session {
			sessions++
		}
		if res.Task {
			tasks++
		}
	}
	return sessions, tasks
}

func (r *rig) wantHeld(sessions, tasks int) {
	r.t.Helper()
	if s, k := r.held(); s != sessions || k != tasks {
		r.t.Fatalf("%d sessions and %d tasks held, want %d and %d: %+v", s, k, sessions, tasks, r.accounts().Reservations)
	}
}

// paidCodex makes Codex reads show an API key route and lets Codex run paid,
// so a Codex pane opens in the paid variant with a priced estimate.
func (r *rig) paidCodex() {
	pa := r.cfg.PaidRuns.Agent("codex")
	pa.Allow, pa.DailyCapUSD = true, 100
	r.cfg.PaidRuns.Set("codex", pa)
	read := r.gate.Cache.Read
	r.gate.Cache.Read = func(ctx context.Context, p corelaunch.Process) (account.Snapshot, error) {
		s, err := read(ctx, p)
		if p.Agent == "codex" {
			s.Billing.RouteSignals = []corelaunch.Signal{{Name: "auth:apiKey", Layer: "account/read"}}
		}
		return s, err
	}
}

// send sends the rig's task to a ready pane.
func (r *rig) send(id, path string) {
	r.t.Helper()
	script := baseScript()
	script["capture"] = screens(sendOK(path)...)
	r.rex(script)
	if err := Send(r.ctx, id, path); err != nil {
		r.t.Fatal(err)
	}
}

// finish lets a wait see the sent task end.
func (r *rig) finish(id string) Entry {
	r.t.Helper()
	script := baseScript()
	script["capture"] = screens(working(), working(), codexIdle(""), codexIdle(""))
	r.rex(script)
	got, err := Wait(r.ctx, id)
	if err != nil || got.State != Ready {
		r.t.Fatalf("%+v %v", got, err)
	}
	return got
}

// A pane open holds, in one reservation, one live slot and one task's
// estimate. The first send takes that task over, with no new spend or slot;
// the second send reserves a task of its own. A pane closed without a send
// gives its task back. A pane that failed to open, or to be recorded, gives
// its holds back only once it is known to be closed; an uncertain one keeps
// them. Reusing a role's live pane takes no new slot.
func TestPeerOpenReservationTransfersOnce(t *testing.T) {
	t.Run("open, first send, second send", func(t *testing.T) {
		r := newRig(t, baseScript())
		r.paidCodex()
		e := r.ready()
		entry := r.entry(e.ID)
		open, ok := r.accounts().Reservations[entry.AccountSessionID]
		if !ok || !open.Session || !open.Task || open.Child.Pane != "block:peer" || !open.Started || open.Estimate.USD <= 0 {
			t.Fatalf("open reservation %q: %+v", entry.AccountSessionID, open)
		}
		if entry.AdmissionID != entry.AccountSessionID || entry.TaskGeneration != "" {
			t.Fatalf("entry after open: %+v", entry)
		}
		_, inflight, err := r.gate.Store.Spent(context.Background(), "codex", open.Created)
		if err != nil {
			t.Fatal(err)
		}
		// Reusing the role's live pane takes nothing new.
		if again := r.open("implement"); again.ID != e.ID {
			t.Fatalf("a second pane opened: %+v", again)
		}
		r.wantHeld(1, 1)

		path := r.task()
		r.send(e.ID, path)
		entry = r.entry(e.ID)
		first, ok := r.accounts().Reservations[entry.TaskGeneration]
		if !ok || first.Session || first.SessionID != entry.AccountSessionID || !first.Started || first.Child.Pane != "block:peer" {
			t.Fatalf("first send reservation %q: %+v", entry.TaskGeneration, first)
		}
		if entry.AdmissionID != entry.TaskGeneration {
			t.Fatalf("admission id %q, generation %q", entry.AdmissionID, entry.TaskGeneration)
		}
		r.wantHeld(1, 1)
		if _, after, _ := r.gate.Store.Spent(context.Background(), "codex", open.Created); after != inflight {
			t.Fatalf("the first send reserved again: %v then %v", inflight, after)
		}

		r.finish(e.ID)
		r.wantHeld(1, 0)
		r.send(e.ID, path)
		entry = r.entry(e.ID)
		second := r.accounts().Reservations[entry.TaskGeneration]
		if entry.TaskGeneration == first.ID || second.Session || !second.Task || second.Estimate.USD <= 0 {
			t.Fatalf("second send: %+v", second)
		}
		r.wantHeld(1, 1)
	})

	t.Run("close without a send", func(t *testing.T) {
		r := newRig(t, baseScript())
		r.paidCodex()
		e := r.ready()
		if err := Close(r.ctx, e.ID); err != nil {
			t.Fatal(err)
		}
		st := r.accounts()
		if len(st.Reservations) != 0 || len(st.Settled) != 0 {
			t.Fatalf("a never-sent open was kept or charged: %+v", st)
		}
	})

	t.Run("pane never opened", func(t *testing.T) {
		script := baseScript()
		script["new"] = testbin.Scenario{Exit: 4, Stderr: "connecting to Rex server\nfailed to WebSocket dial\n"}
		r := newRig(t, script)
		if _, err := Open(r.ctx, "implement"); err == nil {
			t.Fatal("open succeeded")
		}
		if st := r.accounts(); len(st.Reservations) != 0 {
			t.Fatalf("a pane rex never opened kept its hold: %+v", st.Reservations)
		}
	})

	for _, c := range []struct {
		name   string
		close  testbin.Scenario
		closed bool
	}{
		{"unrecorded pane closed", testbin.Scenario{}, true},
		{"unrecorded pane maybe open", testbin.Scenario{Exit: 1, Stderr: "rex: timed out"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			script := baseScript()
			script["block close"] = c.close
			r := newRig(t, script)
			r.useDeps(func(d *Deps) {
				d.RecordPeer = func(*session.Registry, session.Entry, int) error { return errors.New("registry is full") }
			})
			if _, err := Open(r.ctx, "implement"); err == nil {
				t.Fatal("open succeeded without a record")
			}
			held := r.accounts().Reservations
			if c.closed && len(held) != 0 {
				t.Fatalf("a closed pane kept its holds: %+v", held)
			}
			if !c.closed && len(held) != 1 {
				t.Fatalf("a pane that may be open lost its holds: %+v", held)
			}
		})
	}
}

// Observations of the same task, by Status, Wait or Send, and by command
// processes that race, settle its generation once. Ready ends the task but
// keeps the pane's slot; a busy, questioning or unknown screen keeps the task
// held; a gone pane frees its slot and settles an unfinished task at its
// estimate, once. Peers report no token counts, so a paid task counts at its
// estimate, and the note says so.
func TestPeerRepeatedIdleDoesNotDoubleSettle(t *testing.T) {
	t.Run("ready settles once", func(t *testing.T) {
		r := newRig(t, baseScript())
		r.paidCodex()
		e := r.ready()
		path := r.task()
		r.send(e.ID, path)
		gen := r.entry(e.ID).TaskGeneration
		estimate := r.accounts().Reservations[gen].Estimate.USD
		got := r.finish(e.ID)
		if !strings.Contains(got.Note, "estimate") {
			t.Fatalf("a paid task without token counts did not say so: %q", got.Note)
		}
		// A second wait and a status see the same idle pane.
		r.finish(e.ID)
		if _, err := Status(r.ctx, e.ID); err != nil {
			t.Fatal(err)
		}
		st := r.accounts()
		if s, ok := st.Settled[gen]; !ok || len(st.Settled) != 1 || s.ChargedUSD != estimate {
			t.Fatalf("settled %+v, want %s once at %v", st.Settled, gen, estimate)
		}
		r.wantHeld(1, 0)
	})

	t.Run("racing processes", func(t *testing.T) {
		r := newRig(t, baseScript())
		r.paidCodex()
		e := r.ready()
		r.send(e.ID, r.task())
		gen := r.entry(e.ID).TaskGeneration
		var wg sync.WaitGroup
		errs := make([]error, 4)
		for i := range errs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				env, err := setup(r.ctx)
				if err == nil {
					_, err = env.taskEnded(r.ctx, r.entry(e.ID))
				}
				errs[i] = err
			}()
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			t.Fatal(err)
		}
		st := r.accounts()
		if len(st.Settled) != 1 || st.Settled[gen].ChargedUSD <= 0 {
			t.Fatalf("racing settlements: %+v", st.Settled)
		}
		settled, _, err := r.gate.Store.Spent(context.Background(), "codex", st.Settled[gen].Ended)
		if err != nil || settled != st.Settled[gen].ChargedUSD {
			t.Fatalf("charged %v in all (%v)", settled, err)
		}
	})

	t.Run("busy and question keep the task", func(t *testing.T) {
		r := newRig(t, baseScript())
		e := r.ready()
		r.send(e.ID, r.task())
		for _, screen := range []string{working(), fixtureText("codex-trust-live.txt")} {
			script := baseScript()
			script["capture"] = screens(screen)
			r.rex(script)
			if _, err := Status(r.ctx, e.ID); err != nil {
				t.Fatal(err)
			}
			if r.entry(e.ID).TaskGeneration == "" {
				t.Fatal("an unfinished task was settled")
			}
			r.wantHeld(1, 1)
		}
	})

	t.Run("gone pane", func(t *testing.T) {
		r := newRig(t, baseScript())
		r.paidCodex()
		e := r.ready()
		r.send(e.ID, r.task())
		gen := r.entry(e.ID).TaskGeneration
		estimate := r.accounts().Reservations[gen].Estimate.USD
		script := baseScript()
		script["block inspect"] = gone
		r.rex(script)
		for range 2 {
			if st, err := Status(r.ctx, e.ID); err != nil || st.State != Closed {
				t.Fatalf("%+v %v", st, err)
			}
		}
		st := r.accounts()
		if len(st.Reservations) != 0 || len(st.Settled) != 1 || st.Settled[gen].ChargedUSD != estimate {
			t.Fatalf("gone pane: held %+v, settled %+v", st.Reservations, st.Settled)
		}
	})
}

// A pane's env file and argv come from one built peer process. A listed
// API key stays out of a free pane; a paid pane keeps the person's settings
// with no fast pin. Full limits refuse a pane at once with the counts and
// the close command, and a send takes no extra session. An API key question
// always goes to the lead, whatever auto_answers say. Migration notices stay
// (TestMigrationOnceAcrossDelegateAndPeer).
func TestPeerBuildAndPaidQuestion(t *testing.T) {
	t.Run("free pane", func(t *testing.T) {
		r := newRig(t, withScreens(claudeIdle("")))
		t.Setenv("ANTHROPIC_API_KEY", "sk-peer-free")
		r.cfg.EnvPassthrough = append(r.cfg.EnvPassthrough, "ANTHROPIC_API_KEY")
		e := r.open("plan")
		launched := r.started()
		if slices.ContainsFunc(launched, func(w string) bool { return strings.Contains(w, "sk-peer-free") }) {
			t.Fatal("a free pane got a listed key")
		}
		res := r.accounts().Reservations[r.entry(e.ID).AccountSessionID]
		if res.Variant != corelaunch.Free || res.Shape != corelaunch.Peer {
			t.Fatalf("reservation: %+v", res)
		}
		argv := launched[slices.Index(launched, "claude"):]
		if argv[1] != "--settings" || !strings.Contains(argv[2], `"fastMode":false`) || !strings.Contains(argv[2], "notifications_disabled") {
			t.Fatalf("argv does not start with the process's settings: %q", argv)
		}
		if strings.Count(strings.Join(argv, "\x00"), "--settings") != 1 {
			t.Fatalf("two settings: %q", argv)
		}
		if !slices.Contains(launched, "DUOLOG_PEER="+e.ID) {
			t.Fatalf("pane env lacks its peer id: %q", launched)
		}
	})

	t.Run("paid pane", func(t *testing.T) {
		r := newRig(t, withScreens(claudeIdle("")))
		t.Setenv("ANTHROPIC_API_KEY", "sk-peer-paid")
		t.Setenv("OPENAI_API_KEY", "sk-peer-other")
		r.cfg.EnvPassthrough = append(r.cfg.EnvPassthrough, "OPENAI_API_KEY")
		pc := r.cfg.PaidRuns.Agent("claude")
		pc.Allow, pc.DailyCapUSD = true, 100
		pc.Env = []string{"ANTHROPIC_API_KEY"}
		r.cfg.PaidRuns.Set("claude", pc)
		read := r.gate.Cache.Read
		r.gate.Cache.Read = func(ctx context.Context, p corelaunch.Process) (account.Snapshot, error) {
			s, err := read(ctx, p)
			s.Billing.RouteSignals = []corelaunch.Signal{{Name: "auth:apiKey", Layer: "auth status"}}
			return s, err
		}
		e := r.open("plan")
		launched := r.started()
		if !slices.Contains(launched, "ANTHROPIC_API_KEY=sk-peer-paid") || slices.Contains(launched, "OPENAI_API_KEY=sk-peer-other") {
			t.Fatalf("paid pane env: %q", launched)
		}
		argv := launched[slices.Index(launched, "claude"):]
		if strings.Contains(argv[2], "fastMode") {
			t.Fatalf("a paid pane pinned fast mode: %s", argv[2])
		}
		if res := r.accounts().Reservations[r.entry(e.ID).AccountSessionID]; res.Variant != corelaunch.Paid || !res.Paid {
			t.Fatalf("paid reservation: %+v", res)
		}
	})

	t.Run("full limits refuse at once", func(t *testing.T) {
		r := newRig(t, withScreens(codexIdle("")))
		r.cfg.Limits.MaxTotalSessions = 1
		e := r.ready()
		r.send(e.ID, r.task())
		r.wantHeld(1, 1)
		r.rex(withScreens(claudeIdle("")))
		sleeps := len(r.sleeps)
		_, err := Open(r.ctx, "plan")
		if err == nil || !strings.Contains(err.Error(), "sessions 1 of 1 in all") || !strings.Contains(err.Error(), "duolog peer close "+e.ID) {
			t.Fatalf("full limits: %v", err)
		}
		if len(r.spawns()) != 0 || len(r.sleeps) != sleeps {
			t.Fatalf("a refused open made a pane or waited: %v", r.spawns())
		}
	})

	t.Run("API key question goes to the lead", func(t *testing.T) {
		question := "\nDetected a custom API key in your environment\n\nANTHROPIC_API_KEY: sk-ant-...redacted\n\nDo you want to use this API key?\n\n❯ 1. Yes\n  2. No (recommended)\n\nEnter to confirm · Esc to exit\n"
		r := newRig(t, withScreens(question))
		r.cfg.Rex.AutoAnswers = []config.AutoAnswer{{Match: "API key", Answer: "Yes"}, {Match: "use this", Answer: "No (recommended)"}}
		e := r.open("plan")
		if e.State != Question || !strings.Contains(e.Question, "API key") {
			t.Fatalf("%+v", e)
		}
		if keys := r.keys(); len(keys) != 0 {
			t.Fatalf("a billing choice was answered: %v", keys)
		}
		if asks := r.messages("ask"); len(asks) != 1 || !strings.Contains(asks[0].S, "API key") {
			t.Fatalf("the lead was not asked: %+v", asks)
		}
	})
}
