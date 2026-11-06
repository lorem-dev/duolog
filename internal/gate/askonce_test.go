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
	return EscalateOnce(context.Background(), dir, key, 2*time.Second, func() (Decision, bool) {
		*asked++
		return Decision{Kind: Deny, Reason: "asked"}, true
	})
}

func TestAskRecordWaitIsCapped(t *testing.T) {
	dir := t.TempDir()
	key := AskKey("session", "toolu_1", "exec ls")
	start := time.Now().UTC().Add(-MaxAskWait - 10*time.Second)
	writeMemo(t, dir, key, askMemo{Done: true, Start: start, Wait: 999999999, Kind: Allow})
	asked := 0
	if d := escalate(t, dir, key, &asked); d.Kind != Deny || asked != 1 {
		t.Fatalf("decision %+v, asked %d: a wait past MaxAskWait was trusted", d, asked)
	}
}

func TestAskRecordFromTheFutureIsIgnored(t *testing.T) {
	dir := t.TempDir()
	key := AskKey("session", "toolu_1", "exec ls")
	writeMemo(t, dir, key, askMemo{Done: true, Start: time.Now().UTC().Add(time.Hour), Wait: 60, Kind: Allow})
	asked := 0
	if d := escalate(t, dir, key, &asked); d.Kind != Deny || asked != 1 {
		t.Fatalf("decision %+v, asked %d: a start in the future was trusted", d, asked)
	}
}

func TestQuestionKeyIsReusedOnce(t *testing.T) {
	dir := t.TempDir()
	key := AskKey("session", "", "exec rm -rf build")
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

func TestToolUseKeyDiffersFromQuestionKey(t *testing.T) {
	if AskKey("s", "toolu_1", "exec ls") == AskKey("s", "", "exec ls") {
		t.Fatal("keys collide")
	}
	if AskKey("", "", "exec ls") != "" {
		t.Fatal("a key without a session")
	}
}

func TestAskRecordIsReusedInsideItsWait(t *testing.T) {
	dir := t.TempDir()
	key := AskKey("session", "toolu_1", "exec ls")
	writeMemo(t, dir, key, askMemo{Done: true, Start: time.Now().UTC().Add(-time.Second), Wait: 60, Kind: Allow})
	asked := 0
	if d := escalate(t, dir, key, &asked); d.Kind != Allow || asked != 0 {
		t.Fatalf("decision %+v, asked %d", d, asked)
	}
}
