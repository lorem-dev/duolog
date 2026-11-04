package peer

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/router"
	"github.com/lorem-dev/duolog/internal/session"
)

func TestWaitKeepsWatchingUntilQuestionReminder(t *testing.T) {
	r := newRig(t, withScreens(fixtureText("codex-trust-live.txt")))
	e := r.open("implement")
	r.cfg.Rex.StuckNotifyMinutes = 1
	oldSleep, oldNotify := sleep, notifyPerson
	t.Cleanup(func() { sleep = oldSleep; notifyPerson = oldNotify })
	var notified int
	notifyPerson = func(string, string) { notified++ }
	sleep = func(ctx context.Context, _ time.Duration) error {
		_, err := r.reg.Update(e.ID, func(x *session.Entry) { x.QuestionSince = time.Now().Add(-2 * time.Minute) })
		return err
	}
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
	old := notifyPerson
	notifyPerson = func(string, string) { t.Error("answered question caused a reminder") }
	t.Cleanup(func() { notifyPerson = old })
	ctx, cancel := context.WithTimeout(r.ctx, time.Second)
	defer cancel()
	got, err := Wait(ctx, e.ID)
	if err != nil || got.State != Ready || r.entry(e.ID).Asked != "" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestNotificationTextIsNotExecutableSource(t *testing.T) {
	message := "peer-id: a quote \"; $(do not run)\nnext line"
	for _, goos := range []string{"darwin", "linux", "windows"} {
		program, args := notificationArgv(goos, "duolog", message)
		if program == "" || len(args) == 0 {
			t.Fatalf("missing notifier for %s", goos)
		}
		if goos == "windows" {
			if strings.Contains(args[len(args)-1], message) || !strings.Contains(args[len(args)-1], "$env:DUOLOG_NOTIFICATION_TEXT") {
				t.Fatal(args)
			}
		} else if args[len(args)-1] != message {
			t.Fatalf("notification text changed: %v", args)
		}
	}
	if program, _ := notificationArgv("unknown", "duolog", message); program != "" {
		t.Fatal("unsupported OS got a notifier")
	}
}

func TestAgentArgvDisablesNotifications(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			args := agentArgv(router.Route{Target: config.Target{Agent: agent, Model: "sol", Effort: "high"}}, mode.Ask, t.TempDir())
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
	old := notifyPerson
	notifyPerson = func(_ string, message string) { messages = append(messages, message) }
	t.Cleanup(func() { notifyPerson = old })
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
