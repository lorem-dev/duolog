package peer

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/mode"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
)

func TestWaitKeepsWatchingUntilQuestionReminder(t *testing.T) {
	r := newRig(t, withScreens(fixtureText("codex-trust-live.txt")))
	e := r.open("implement")
	r.cfg.Rex.StuckNotifyMinutes = 1
	var notified int
	r.useDeps(func(d *Deps) {
		d.Notify = func(string, string) { notified++ }
		d.Clock = funcClock(func(ctx context.Context, _ time.Duration) error {
			_, err := r.reg.Update(e.ID, func(x *session.Entry) { x.QuestionSince = time.Now().Add(-2 * time.Minute) })
			return err
		})
	})
	ctx, cancel := context.WithTimeout(r.ctx, time.Second)
	defer cancel()
	got, err := Wait(ctx, e.ID)
	if err != nil || got.State != Question || notified != 1 {
		t.Fatalf("%+v %v notifications=%d", got, err, notified)
	}
	if _, err := Wait(ctx, e.ID); err != nil || notified != 1 {
		t.Fatalf("duplicate notification: %v %d", err, notified)
	}
}

func TestWaitDoesNotRemindForAnsweredQuestion(t *testing.T) {
	r := newRig(t, withScreens(fixtureText("codex-trust-live.txt")))
	e := r.open("implement")
	r.cfg.Rex.StuckNotifyMinutes = 1
	r.rex(withScreens(fixtureText("codex-trust-live.txt"), codexIdle(""), codexIdle("")))
	r.useDeps(func(d *Deps) { d.Notify = func(string, string) { t.Error("answered question caused a reminder") } })
	ctx, cancel := context.WithTimeout(r.ctx, time.Second)
	defer cancel()
	got, err := Wait(ctx, e.ID)
	if err != nil || got.State != Ready || r.entry(e.ID).Asked != "" {
		t.Fatalf("%+v %v", got, err)
	}
}

// funcClock is real time whose Sleep is f.
type funcClock func(ctx context.Context, d time.Duration) error

func (funcClock) Now() time.Time { return time.Now() }

func (f funcClock) Sleep(ctx context.Context, d time.Duration) error { return f(ctx, d) }

func TestAgentArgvDisablesNotifications(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			route := coreroute.Route{Target: config.Target{Agent: agent, Model: "sol", Effort: "high"}}
			args := argvFor(route, mode.Ask, peerProcess(t, &config.Config{}, route, corelaunch.Free, t.TempDir()))
			if agent == "codex" {
				i := slices.Index(args, "tui.notifications=false")
				if i < 1 || args[i-1] != "-c" {
					t.Fatalf("notifications enabled: %v", args)
				}
				if !slices.Contains(args, "notify=[]") {
					t.Fatal("external notify command inherited", args)
				}
				return
			}
			i := slices.Index(args, "--settings")
			if i < 0 || i+1 == len(args) {
				t.Fatalf("missing settings: %v", args)
			}
			var settings map[string]any
			if err := json.Unmarshal([]byte(args[i+1]), &settings); err != nil {
				t.Fatal(err)
			}
			if settings["preferredNotifChannel"] != "notifications_disabled" {
				t.Fatalf("%+v", settings)
			}
		})
	}
}

func TestStuckQuestionNotificationIsOnceAndResets(t *testing.T) {
	r := newRig(t, withScreens(fixtureText("codex-trust-live.txt")))
	r.cfg.Rex.StuckNotifyMinutes = 5
	e := r.open("implement")
	entry := r.entry(e.ID)
	if entry.QuestionSince.IsZero() {
		t.Fatal("question has no start time")
	}
	var messages []string
	r.useDeps(func(d *Deps) { d.Notify = func(_ string, message string) { messages = append(messages, message) } })
	peerEnv, err := setup(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := peerEnv.notifyStuck(r.ctx, entry, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatal("notified before threshold")
	}
	if _, err := r.reg.Update(entry.ID, func(x *session.Entry) { x.QuestionSince = time.Now().Add(-6 * time.Minute) }); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := peerEnv.notifyStuck(r.ctx, entry, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if len(messages) != 1 || !strings.Contains(messages[0], entry.ID) || !strings.Contains(messages[0], "Trust") || strings.Contains(messages[0], "\n") {
		t.Fatal(messages)
	}
	if err := peerEnv.ask(r.entry(entry.ID), "Another question?\n1. Yes"); err != nil {
		t.Fatal(err)
	}
	if r.entry(entry.ID).QuestionNotified {
		t.Fatal("new question retained notification flag")
	}
	if _, err := r.reg.Update(entry.ID, func(x *session.Entry) { x.QuestionSince = time.Now().Add(-6 * time.Minute) }); err != nil {
		t.Fatal(err)
	}
	if err := peerEnv.notifyStuck(r.ctx, r.entry(entry.ID), time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatal(messages)
	}
	if _, err := r.reg.Update(entry.ID, func(x *session.Entry) { x.Asked = "" }); err != nil {
		t.Fatal(err)
	}
	if err := peerEnv.notifyStuck(r.ctx, entry, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatal("answered question notified again")
	}
}
