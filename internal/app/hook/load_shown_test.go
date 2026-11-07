package hook

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/store"
)

func TestLoadDueSaysOnceAndAgainAfterFiveMinutesOrAChange(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	a, b := "duolog peers: claude 3 running of 12", "duolog peers: claude 4 running of 12"
	if !loadDue(dir, "lead1", a, now) {
		t.Fatal("the first line is due")
	}
	if loadDue(dir, "lead1", a, now.Add(time.Minute)) {
		t.Fatal("the same line one minute later is not due")
	}
	if !loadDue(dir, "lead1", b, now.Add(2*time.Minute)) {
		t.Fatal("a changed number is due at once")
	}
	if loadDue(dir, "lead1", b, now.Add(6*time.Minute-time.Second)) {
		t.Fatal("not due before 5 minutes")
	}
	if !loadDue(dir, "lead1", b, now.Add(7*time.Minute)) {
		t.Fatal("due 5 minutes after the last one")
	}
	if !loadDue(dir, "lead2", b, now.Add(7*time.Minute)) {
		t.Fatal("each lead has its own clock")
	}
}

// Hooks of many leads mark at once: every mark stays, and the same line is due
// to one delivery of a lead only, never to two that run together.
func TestLoadMarkAndDueHoldUnderConcurrentHooks(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	line := "duolog peers: claude 3 running of 12"
	var wg sync.WaitGroup
	var due atomic.Int32
	for i := range 16 {
		wg.Go(func() { loadMark(dir, fmt.Sprintf("lead%d", i), line, now) })
		wg.Go(func() {
			if loadDue(dir, "shared", line, now) {
				due.Add(1)
			}
		})
	}
	wg.Wait()
	if got := due.Load(); got != 1 {
		t.Fatalf("the same line was due %d times", got)
	}
	for i := range 16 {
		if loadDue(dir, fmt.Sprintf("lead%d", i), line, now.Add(time.Minute)) {
			t.Fatalf("lead%d lost its mark", i)
		}
	}
}

// A hook of another lead that holds its record past the notice wait, as a
// slow write and rename on Windows does, costs this lead no mark: each lead
// has its own record and lock.
func TestLoadMarkWaitsForNoOtherLead(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, shownDir), 0o700); err != nil {
		t.Fatal(err)
	}
	unlock, err := store.LockFile(shownLock(dir, "lead2"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unlock() }()
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	line := "duolog peers: claude 3 running of 12"
	loadMark(dir, "lead1", line, now)
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if loadDue(dir, "lead1", line, now.Add(time.Minute)) {
		t.Fatal("lead1 lost its mark while lead2's record was held")
	}
}

func TestLoadMarkRecordsALineSeenByPrompt(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	line := "duolog peers: claude 3 running of 12"
	loadMark(dir, "lead1", line, now)
	if loadDue(dir, "lead1", line, now.Add(time.Minute)) {
		t.Fatal("a line the prompt hook just showed is not due again")
	}
}
