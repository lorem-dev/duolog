package gate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAskWaitFollowsTheAskAndItsHook(t *testing.T) {
	const installed = 630 * time.Second
	for _, tc := range []struct {
		name    string
		seconds int
		person  bool
		hook    time.Duration
		want    time.Duration
	}{
		{"lead ask keeps its short cap", 900, false, installed, MaxAskWait},
		{"lead ask below the cap", 2, false, installed, 2 * time.Second},
		{"person ask gets ten minutes", 600, true, installed, 10 * time.Minute},
		{"person ask is capped", 5000, true, installed, MaxPersonAskWait},
		{"person ask below the cap", 45, true, installed, 45 * time.Second},
		{"an old hook keeps the old wait", 600, true, LegacyHookBudget, MaxAskWait},
		{"a hook at the agent's default stays inside it", 600, true, 600 * time.Second, 570 * time.Second},
		{"a short hook shortens a lead ask", 120, false, 60 * time.Second, 30 * time.Second},
		{"a hook too short to wait in denies at once", 600, true, 10 * time.Second, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := AskWait(tc.seconds, tc.person, tc.hook); got != tc.want {
				t.Fatalf("AskWait(%d, %v, %s) = %s, want %s", tc.seconds, tc.person, tc.hook, got, tc.want)
			}
		})
	}
}

// A hook that waited on another hook's ask has spent part of its budget, so
// the ask it then makes gets only the rest.
func TestLaterHookAsksWithTheTimeLeft(t *testing.T) {
	dir := t.TempDir()
	key := AskKey("session", "toolu_1", "exec ls", true)
	// A pending ask whose asker is gone: it stops counting within one poll.
	writeMemo(t, dir, key, askMemo{Start: time.Now().UTC().Add(-askGrace + 100*time.Millisecond), Wait: 0})
	const wait = 1500 * time.Millisecond
	var left time.Duration
	d := EscalateOnce(context.Background(), dir, key, wait, func(l time.Duration) (Decision, bool) {
		left = l
		return Decision{Kind: Deny, Reason: "asked"}, true
	})
	if d.Reason != "asked" || left <= 0 || left > wait-100*time.Millisecond {
		t.Fatalf("decision %+v, asked with %s of %s", d, left, wait)
	}
}

func TestFirstHookAsksWithTheWholeWait(t *testing.T) {
	var left time.Duration
	const wait = 10 * time.Second
	EscalateOnce(context.Background(), t.TempDir(), AskKey("session", "toolu_1", "exec ls", true), wait, func(l time.Duration) (Decision, bool) {
		left = l
		return Decision{Kind: Deny}, true
	})
	if left < wait-time.Second || left > wait {
		t.Fatalf("asked with %s of %s", left, wait)
	}
}

// PermissionRequest follows PreToolUse for one tool call, and must reuse an
// answer the person gave after the lead's two minutes.
func TestPersonReplyIsReusedPastTheLeadWait(t *testing.T) {
	dir := t.TempDir()
	key := AskKey("session", "toolu_1", "exec ls", true)
	writeMemo(t, dir, key, askMemo{Done: true, Start: time.Now().UTC().Add(-MaxAskWait - time.Minute), Wait: 600, Kind: Allow})
	asked := 0
	if d := escalate(t, dir, key, &asked); d.Kind != Allow || asked != 0 {
		t.Fatalf("decision %+v, asked %d", d, asked)
	}
}

// A pending person-only ask outlasts the old lead budget, so another hook's
// clean-up must leave its record.
func TestForgetStaleKeepsAPendingPersonAsk(t *testing.T) {
	dir := t.TempDir()
	key := AskKey("session", "toolu_1", "exec ls", true)
	writeMemo(t, dir, key, askMemo{Start: time.Now().UTC().Add(-5 * time.Minute), Wait: 600})
	folder := filepath.Join(dir, "asks")
	path := filepath.Join(folder, key+".json")
	old := time.Now().Add(-5 * time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	forgetStale(folder)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("record of a live ask removed: %v", err)
	}
	gone := time.Now().Add(-MaxPersonAskWait - 2*time.Minute)
	if err := os.Chtimes(path, gone, gone); err != nil {
		t.Fatal(err)
	}
	forgetStale(folder)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("record past every wait kept: %v", err)
	}
}
