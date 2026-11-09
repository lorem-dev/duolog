package delegate

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	claudeText = `{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"Looking around."}]}}`
	claudeTool = `{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}`
	codexText  = `{"type":"item.completed","item":{"id":"i1","type":"agent_message","text":"Codex says hi."}}`
	codexTool  = `{"type":"item.started","item":{"id":"i2","type":"command_execution","command":"ls"}}`
)

// watchFixture is a running record whose log is in the work root.
func watchFixture(t *testing.T) (recordPath, logPath string, rec Record) {
	t.Helper()
	work := t.TempDir()
	rec = sampleRecord("d-abcdef", time.Now().UTC())
	rec.WorkRoot, rec.PID = work, 0
	rec.Agent, rec.Model = "claude", "sonnet"
	rec.Log = ".duolog/tasks/task-1/stdout.log"
	logPath = filepath.Join(work, filepath.FromSlash(rec.Log))
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	recordPath = filepath.Join(RecordDir(t.TempDir()), rec.ID+".json")
	if err := WriteRecord(recordPath, rec); err != nil {
		t.Fatal(err)
	}
	return recordPath, logPath, rec
}

func appendTo(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

// syncBuffer is a buffer that the watching goroutine and the test share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(2 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestWatchStartsAtTheBeginningFollowsGrowthAndEndsWithTheRun(t *testing.T) {
	recordPath, logPath, rec := watchFixture(t)
	// A first line is there before the watch starts; the last has no newline yet.
	appendTo(t, logPath, claudeText+"\n"+`{"type":"assis`)
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- Watch(context.Background(), out, recordPath, nil, time.Millisecond) }()

	waitFor(t, "the first line", func() bool { return strings.Contains(out.String(), "Looking around.") })
	appendTo(t, logPath, `tant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}`+"\n"+codexText+"\n"+codexTool+"\n")
	waitFor(t, "the grown lines", func() bool { return strings.Contains(out.String(), "> command ls") })

	rec.State, rec.Ended, rec.Line = "done", time.Now().UTC(), "done=true"
	if err := WriteRecord(recordPath, rec); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not end with the run")
	}
	want := []string{"d-abcdef implement claude/sonnet", "Looking around.", "> Bash go test ./...", "Codex says hi.", "> command ls", "ended: done"}
	got := strings.Split(strings.TrimSpace(out.String()), "\n")
	for i, w := range want {
		if i >= len(got) || !strings.HasPrefix(got[i], w) {
			t.Fatalf("line %d of %q, want prefix %q", i, got, w)
		}
	}
}

func TestWatchReadsTheLastLineWithoutNewlineWhenTheRunEnds(t *testing.T) {
	recordPath, logPath, rec := watchFixture(t)
	appendTo(t, logPath, claudeTool)
	rec.State, rec.Ended = "failed", time.Now().UTC()
	if err := WriteRecord(recordPath, rec); err != nil {
		t.Fatal(err)
	}
	out := &syncBuffer{}
	if err := Watch(context.Background(), out, recordPath, nil, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "> Bash go test ./...") || !strings.Contains(out.String(), "ended: failed") {
		t.Fatalf("output %q", out.String())
	}
}

func TestWatchStopsCleanlyOnCancel(t *testing.T) {
	recordPath, logPath, _ := watchFixture(t)
	appendTo(t, logPath, claudeText+"\n")
	ctx, cancel := context.WithCancel(context.Background())
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- Watch(ctx, out, recordPath, nil, time.Millisecond) }()
	waitFor(t, "the line", func() bool { return strings.Contains(out.String(), "Looking around.") })
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancelled watch returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch ignored the cancel")
	}
}

func TestWatchWaitsForALogThatStartsLaterAndSurvivesATruncation(t *testing.T) {
	recordPath, logPath, rec := watchFixture(t)
	rec.Log = ""
	if err := WriteRecord(recordPath, rec); err != nil {
		t.Fatal(err)
	}
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- Watch(context.Background(), out, recordPath, nil, time.Millisecond) }()
	time.Sleep(20 * time.Millisecond)
	rec.Log = ".duolog/tasks/task-1/stdout.log"
	appendTo(t, logPath, claudeText+"\n"+claudeText+"\n")
	if err := WriteRecord(recordPath, rec); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first log", func() bool { return strings.Contains(out.String(), "Looking around.") })
	// A login retry starts the log over.
	if err := os.WriteFile(logPath, []byte(codexText+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the restarted log", func() bool { return strings.Contains(out.String(), "Codex says hi.") })
	rec.State, rec.Ended = "done", time.Now().UTC()
	if err := WriteRecord(recordPath, rec); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWatchOfARunThatNeverLoggedSaysSo(t *testing.T) {
	recordPath, _, rec := watchFixture(t)
	rec.Log, rec.State, rec.Ended, rec.Line = "", "failed", time.Now().UTC(), "refused: no quota"
	if err := WriteRecord(recordPath, rec); err != nil {
		t.Fatal(err)
	}
	out := &syncBuffer{}
	if err := Watch(context.Background(), out, recordPath, nil, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no log") || !strings.Contains(out.String(), "ended: failed") {
		t.Fatalf("output %q", out.String())
	}
}

// The end line drops C1 controls, as it drops C0 ones.
func TestWatchEndLineDropsC1Controls(t *testing.T) {
	recordPath, _, rec := watchFixture(t)
	rec.Log, rec.State, rec.Ended, rec.Line = "", "failed", time.Now().UTC(), "bad\u009b2Jtext\u0085more\x1b[0m"
	if err := WriteRecord(recordPath, rec); err != nil {
		t.Fatal(err)
	}
	out := &syncBuffer{}
	if err := Watch(context.Background(), out, recordPath, nil, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), "\u009b\u0085\x1b") || !strings.Contains(out.String(), "ended: failed: bad2Jtextmore[0m") {
		t.Fatalf("output %q", out.String())
	}
}

// The result line the lead reads drops C1 controls from the summary.
func TestResultLineDropsC1Controls(t *testing.T) {
	line := resultLine(true, "", "ok\u009b2J done\x1b[0m", "", "", "answer=x")
	if strings.ContainsAny(line, "\u009b\x1b") || !strings.Contains(line, "summary=ok2J done[0m") {
		t.Fatalf("line %q", line)
	}
}

func TestWatchTreatsADeadProcessAsEnded(t *testing.T) {
	recordPath, logPath, rec := watchFixture(t)
	rec.PID = 4242
	if err := WriteRecord(recordPath, rec); err != nil {
		t.Fatal(err)
	}
	appendTo(t, logPath, claudeText+"\n")
	out := &syncBuffer{}
	if err := Watch(context.Background(), out, recordPath, func(int) bool { return false }, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ended: lost") {
		t.Fatalf("output %q", out.String())
	}
}
