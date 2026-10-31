package bus

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
)

func testWire() config.Wire {
	return config.Wire{MaxSummaryBytes: 200, MaxLineBytes: 512}
}

func openTestBus(t *testing.T) (*Bus, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".duolog")
	b, err := Open(dir, testWire())
	if err != nil {
		t.Fatal(err)
	}
	return b, dir
}

func sendTestMessage(t *testing.T, b *Bus, m Message) int64 {
	t.Helper()
	n, err := b.Send(m)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func readBusFile(t *testing.T, dir string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "bus.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSendAssignsSequentialN(t *testing.T) {
	b, dir := openTestBus(t)
	for i := int64(1); i <= 3; i++ {
		// Reopening must resume the on-disk sequence, not a process counter.
		b, err := Open(dir, testWire())
		if err != nil {
			t.Fatal(err)
		}
		if n := sendTestMessage(t, b, Message{N: 99, From: "claude/worker", To: "codex/lead", K: "status", S: "ready"}); n != i {
			t.Fatalf("N = %d, want %d", n, i)
		}
	}
	lines, err := b.Inbox("codex/lead")
	if err != nil || len(lines) != 3 {
		t.Fatalf("Inbox: %d lines, %v", len(lines), err)
	}
	for i, line := range lines {
		var m Message
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatal(err)
		}
		if m.N != int64(i+1) {
			t.Fatalf("stored N = %d", m.N)
		}
		if _, err := time.Parse(time.RFC3339Nano, m.T); err != nil {
			t.Fatalf("timestamp %q: %v", m.T, err)
		}
	}
}

func TestInboxReturnsExactStoredBytes(t *testing.T) {
	b, dir := openTestBus(t)
	stored := []byte("{ \"s\":\"<tag>&\", \"to\":\"reader\", \"n\":1, \"from\":\"writer\", \"extra\": true }\n")
	if err := os.WriteFile(filepath.Join(dir, "bus.jsonl"), stored, 0600); err != nil {
		t.Fatal(err)
	}
	lines, err := b.Inbox("reader")
	if err != nil || len(lines) != 1 || !bytes.Equal(lines[0], stored) {
		t.Fatalf("Inbox = %q, %v; want exact %q", lines, err, stored)
	}
}

func TestInboxReturnsOnlyNew(t *testing.T) {
	b, dir := openTestBus(t)
	sendTestMessage(t, b, Message{From: "writer", To: "reader", S: "first"})
	sendTestMessage(t, b, Message{From: "writer", To: "reader", S: "also first"})
	if lines, err := b.Inbox("reader"); err != nil || len(lines) != 2 {
		t.Fatalf("first Inbox = %q, %v", lines, err)
	}
	// Corrupt the consumed prefix: the next call must seek past it.
	f, err := os.OpenFile(filepath.Join(dir, "bus.jsonl"), os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.WriteAt([]byte("!"), 0)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("corrupt prefix: %v, %v", writeErr, closeErr)
	}
	b, err = Open(dir, testWire())
	if err != nil {
		t.Fatal(err)
	}
	if lines, err := b.Inbox("reader"); err != nil || len(lines) != 0 {
		t.Fatalf("second Inbox = %q, %v", lines, err)
	}
	sendTestMessage(t, b, Message{From: "writer", To: "reader", S: "second"})
	if lines, err := b.Inbox("reader"); err != nil || len(lines) != 1 || !bytes.Contains(lines[0], []byte("second")) {
		t.Fatalf("new Inbox = %q, %v", lines, err)
	}
}

func TestInboxFiltersByAddressee(t *testing.T) {
	b, dir := openTestBus(t)
	reader := "codex/lead.3fa9c1"
	for _, m := range []Message{
		{From: "writer", To: "someone/else", S: "other"},
		{From: "writer", To: reader, S: "direct"},
		{From: "writer", To: "*", S: "broadcast"},
		{From: reader, To: reader, S: "self direct"},
		{From: reader, To: "*", S: "self broadcast"},
		{From: "writer", To: "someone/else", S: "last other"},
	} {
		sendTestMessage(t, b, m)
	}
	lines, err := b.Inbox(reader)
	if err != nil || len(lines) != 2 {
		t.Fatalf("Inbox = %q, %v", lines, err)
	}
	for i, want := range []string{"direct", "broadcast"} {
		var m Message
		if err := json.Unmarshal(lines[i], &m); err != nil || m.S != want {
			t.Fatalf("message = %+v, %v; want %q", m, err, want)
		}
	}
	cursor, err := os.ReadFile(filepath.Join(dir, "cursors", url.PathEscape(reader)))
	if err != nil || strings.TrimSpace(string(cursor)) != strconv.Itoa(len(readBusFile(t, dir))) {
		t.Fatalf("cursor = %q, %v; must advance past all messages", cursor, err)
	}
}

func TestLongSummaryRefused(t *testing.T) {
	b, _ := openTestBus(t)
	for _, summary := range []string{strings.Repeat("a", 201), strings.Repeat("é", 101)} {
		_, err := b.Send(Message{S: summary})
		if !errors.Is(err, ErrTooLong) || !strings.Contains(err.Error(), "duolog put") {
			t.Fatalf("Send error = %v, want ErrTooLong mentioning duolog put", err)
		}
	}
	if n := sendTestMessage(t, b, Message{S: strings.Repeat("a", 200)}); n != 1 {
		t.Fatalf("rejected messages consumed sequence numbers: %d", n)
	}
}

func TestConcurrentProcessesDoNotInterleave(t *testing.T) {
	if dir := os.Getenv("DUOLOG_BUS_TEST_DIR"); dir != "" {
		b, err := Open(dir, testWire())
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 200; i++ {
			sendTestMessage(t, b, Message{From: fmt.Sprint(os.Getpid()), To: "*", K: "status", S: fmt.Sprint(i)})
		}
		return
	}
	_, dir := openTestBus(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	commands := make([]*exec.Cmd, 8)
	outputs := make([]bytes.Buffer, len(commands))
	for i := range commands {
		cmd := exec.Command(executable, "-test.run=^TestConcurrentProcessesDoNotInterleave$", "-test.timeout=60s")
		cmd.Env = append(os.Environ(), "DUOLOG_BUS_TEST_DIR="+dir)
		cmd.Stdout, cmd.Stderr = &outputs[i], &outputs[i]
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		commands[i] = cmd
		t.Cleanup(func() { _ = cmd.Process.Kill() })
	}
	for i, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Errorf("child %d: %v\n%s", i, err, &outputs[i])
		}
	}
	data := readBusFile(t, dir)
	if !bytes.HasSuffix(data, []byte("\n")) {
		t.Fatal("unterminated line")
	}
	lines := bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
	if len(lines) != 1600 {
		t.Fatalf("%d lines, want 1600", len(lines))
	}
	for i, line := range lines {
		var m Message
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		if m.N != int64(i+1) {
			t.Fatalf("line %d: N = %d", i, m.N)
		}
	}
}

func TestTornLineNotConsumed(t *testing.T) {
	b, dir := openTestBus(t)
	sendTestMessage(t, b, Message{From: "writer", To: "reader", S: "complete"})
	complete := readBusFile(t, dir)
	tail := []byte(`{"n":2,"from":"writer","to":"reader","s":"torn"}`)
	if err := os.WriteFile(filepath.Join(dir, "bus.jsonl"), append(complete, tail...), 0600); err != nil {
		t.Fatal(err)
	}
	if lines, err := b.Inbox("reader"); err != nil || len(lines) != 1 {
		t.Fatalf("Inbox = %q, %v", lines, err)
	}
	cursorPath := filepath.Join(dir, "cursors", "reader")
	before, err := os.ReadFile(cursorPath)
	if err != nil || strings.TrimSpace(string(before)) != strconv.Itoa(len(complete)) {
		t.Fatalf("cursor = %q, %v", before, err)
	}
	if lines, err := b.Inbox("reader"); err != nil || len(lines) != 0 {
		t.Fatalf("torn Inbox = %q, %v", lines, err)
	}
	after, err := os.ReadFile(cursorPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("cursor moved: %q -> %q, %v", before, after, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bus.jsonl"), append(append(complete, tail...), '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	lines, err := b.Inbox("reader")
	if err != nil || len(lines) != 1 || !bytes.Equal(lines[0], append(tail, '\n')) {
		t.Fatalf("completed Inbox = %q, %v", lines, err)
	}
}

func TestNonASCIIRoundTrips(t *testing.T) {
	b, dir := openTestBus(t)
	want := Message{From: "claude/worker", To: "codex/lead", K: "status", Th: "thread", Ref: "artifacts/answer.txt", S: "Готово 世界 👋 <tag>&"}
	sendTestMessage(t, b, want)
	data := readBusFile(t, dir)
	if !bytes.Contains(data, []byte(want.S)) || bytes.Contains(data, []byte(`\u003c`)) {
		t.Fatalf("summary was escaped: %s", data)
	}
	lines, err := b.Inbox(want.To)
	if err != nil || len(lines) != 1 {
		t.Fatalf("Inbox = %q, %v", lines, err)
	}
	var got Message
	if err := json.Unmarshal(lines[0], &got); err != nil {
		t.Fatal(err)
	}
	want.N, want.T = got.N, got.T
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestSendRepairsTornTail(t *testing.T) {
	for _, completeCount := range []int{0, 2} {
		t.Run(strconv.Itoa(completeCount), func(t *testing.T) {
			b, dir := openTestBus(t)
			for i := 0; i < completeCount; i++ {
				sendTestMessage(t, b, Message{To: "reader", S: "complete"})
			}
			data, err := os.ReadFile(filepath.Join(dir, "bus.jsonl"))
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			fragment := []byte(`{"n":3,"s":"` + strings.Repeat("x", 9000))
			if err := os.WriteFile(filepath.Join(dir, "bus.jsonl"), append(data, fragment...), 0600); err != nil {
				t.Fatal(err)
			}
			if n := sendTestMessage(t, b, Message{To: "reader", S: "repaired"}); n != int64(completeCount+1) {
				t.Fatalf("N = %d", n)
			}
			lines := bytes.Split(bytes.TrimSuffix(readBusFile(t, dir), []byte("\n")), []byte("\n"))
			if len(lines) != completeCount+1 {
				t.Fatalf("lines = %d", len(lines))
			}
			for i, line := range lines {
				var m Message
				if err := json.Unmarshal(line, &m); err != nil || m.N != int64(i+1) {
					t.Fatalf("line %d = %s, %v", i, line, err)
				}
			}
		})
	}
}

func TestCursorForSlashedAddress(t *testing.T) {
	b, dir := openTestBus(t)
	reader := "codex/lead.3fa9c1"
	sendTestMessage(t, b, Message{From: "claude/worker", To: reader, S: "ready"})
	if lines, err := b.Inbox(reader); err != nil || len(lines) != 1 {
		t.Fatalf("Inbox = %q, %v", lines, err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "cursors"))
	if err != nil || len(entries) != 1 || entries[0].IsDir() || entries[0].Name() != url.PathEscape(reader) {
		t.Fatalf("cursor entries = %v, %v", entries, err)
	}
}

func TestOpenRejectsLimitsAboveProtocolMaxima(t *testing.T) {
	for _, tc := range []struct {
		name string
		wire config.Wire
	}{
		{"summary", config.Wire{MaxSummaryBytes: 201, MaxLineBytes: 512}},
		{"line", config.Wire{MaxSummaryBytes: 200, MaxLineBytes: 513}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Open(t.TempDir(), tc.wire); err == nil {
				t.Fatalf("Open accepted limits above protocol maxima: %+v", tc.wire)
			}
		})
	}
}

func TestWireLimits(t *testing.T) {
	b, err := Open(t.TempDir(), config.Wire{MaxSummaryBytes: 3, MaxLineBytes: 150})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Send(Message{S: "four"}); !errors.Is(err, ErrTooLong) {
		t.Fatalf("summary limit: %v", err)
	}
	if _, err := b.Send(Message{S: "ok", Ref: strings.Repeat("a", 150)}); !errors.Is(err, ErrTooLong) || !strings.Contains(err.Error(), "duolog put") {
		t.Fatalf("line limit: %v", err)
	}
	if n := sendTestMessage(t, b, Message{S: "ok"}); n != 1 {
		t.Fatalf("N = %d", n)
	}
}
