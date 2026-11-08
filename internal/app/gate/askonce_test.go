package gate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeMemo puts a record for key under dir, as a forged or old file would be.
func writeMemo(t *testing.T, dir, key string, m askMemo) {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "asks"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asks", key+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// escalate runs EscalateOnce with an ask that denies and counts its calls.
func escalate(t *testing.T, dir, key string, asked *int) Decision {
	t.Helper()
	return EscalateOnce(context.Background(), dir, key, 2*time.Second, func(time.Duration) (Decision, bool) {
		*asked++
		return Decision{Kind: Deny, Reason: "asked"}, true
	})
}

func TestAskRecordWaitIsCapped(t *testing.T) {
	dir := t.TempDir()
	key := AskKey("session", "toolu_1", "exec ls", false)
	start := time.Now().UTC().Add(-MaxPersonAskWait - 10*time.Second)
	writeMemo(t, dir, key, askMemo{Done: true, Start: start, Wait: 999999999, Kind: Allow})
	asked := 0
	if d := escalate(t, dir, key, &asked); d.Kind != Deny || asked != 1 {
		t.Fatalf("decision %+v, asked %d: a wait past MaxPersonAskWait was trusted", d, asked)
	}
}

func TestAskRecordFromTheFutureIsIgnored(t *testing.T) {
	dir := t.TempDir()
	key := AskKey("session", "toolu_1", "exec ls", false)
	writeMemo(t, dir, key, askMemo{Done: true, Start: time.Now().UTC().Add(time.Hour), Wait: 60, Kind: Allow})
	asked := 0
	if d := escalate(t, dir, key, &asked); d.Kind != Deny || asked != 1 {
		t.Fatalf("decision %+v, asked %d: a start in the future was trusted", d, asked)
	}
}

func TestQuestionKeyIsReusedOnce(t *testing.T) {
	dir := t.TempDir()
	key := AskKey("session", "", "exec rm -rf build", false)
	asked := 0
	for i, want := range []int{1, 1, 2, 2} {
		escalate(t, dir, key, &asked)
		if asked != want {
			t.Fatalf("call %d: asked %d times, want %d", i+1, asked, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "asks", key+".json")); !os.IsNotExist(err) {
		t.Fatalf("record left after its reuse: %v", err)
	}
}

// A key from the question can name another call, so a person's long wait does
// not make its answer last longer: reuse ends 120 seconds after the ask began.
// A tool_use_id names one call, and keeps the whole wait.
func TestQuestionKeyReuseStaysWithinTheOldWait(t *testing.T) {
	question := AskKey("session", "", "exec ls", true)
	call := AskKey("session", "toolu_1", "exec ls", true)
	for _, tc := range []struct {
		name, key string
		age       time.Duration
		reused    bool
	}{
		{"question, five minutes", question, 5 * time.Minute, false},
		{"question, past the bound", question, questionReuse + time.Second, false},
		{"question, inside the bound", question, questionReuse - 30*time.Second, true},
		{"tool use id, five minutes", call, 5 * time.Minute, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeMemo(t, dir, tc.key, askMemo{Done: true, Start: time.Now().UTC().Add(-tc.age), Wait: 600, Kind: Allow})
			asked := 0
			d := escalate(t, dir, tc.key, &asked)
			if tc.reused && (d.Kind != Allow || asked != 0) || !tc.reused && (d.Kind != Deny || asked != 1) {
				t.Fatalf("decision %+v, asked %d, want reused=%v", d, asked, tc.reused)
			}
		})
	}
}

func TestToolUseKeyDiffersFromQuestionKey(t *testing.T) {
	if AskKey("s", "toolu_1", "exec ls", false) == AskKey("s", "", "exec ls", false) {
		t.Fatal("keys collide")
	}
	for _, id := range []string{"toolu_1", ""} {
		if AskKey("s", id, "exec ls", false) == AskKey("s", id, "exec ls", true) {
			t.Fatalf("tool use id %q: a person-only ask shares the lead's key", id)
		}
	}
	if AskKey("", "", "exec ls", false) != "" {
		t.Fatal("a key without a session")
	}
}

func TestAskRecordIsReusedInsideItsWait(t *testing.T) {
	dir := t.TempDir()
	key := AskKey("session", "toolu_1", "exec ls", false)
	writeMemo(t, dir, key, askMemo{Done: true, Start: time.Now().UTC().Add(-time.Second), Wait: 60, Kind: Allow})
	asked := 0
	if d := escalate(t, dir, key, &asked); d.Kind != Allow || asked != 0 {
		t.Fatalf("decision %+v, asked %d", d, asked)
	}
}
