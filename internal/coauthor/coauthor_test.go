package coauthor

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
)

func settings() config.Coauthors {
	return config.Coauthors{Enabled: true, Templates: map[string]string{"claude": "{name} <noreply@anthropic.com>", "codex": "OpenAI Codex {model} <noreply@openai.com>"}}
}

func TestRecordsAndLines(t *testing.T) {
	t.Setenv("DUOLOG_DEPTH", "0")
	dir := t.TempDir()
	cfg := settings()
	cutoff := time.Now().UTC().Add(-time.Hour)
	for _, r := range []Record{
		{Time: cutoff, Agent: "codex", Model: "old", Files: 1},
		{Agent: "claude", Model: "claude-opus-5-5", Files: 2, Peer: "peer-a"},
		{Agent: "claude", Model: "claude-opus-5-5", Files: 2, Peer: "peer-a"},
		{Agent: "codex", Model: "gpt-6.1-sol", Files: 1},
		{Agent: "codex", Model: "gpt-6.1-sol", Files: 1},
		{Agent: "other", Model: "unknown", Files: 1},
		{Agent: "codex", Model: "empty", Files: 0},
	} {
		if err := Append(dir, cfg, r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Lines(dir, cfg, cutoff)
	want := []string{"Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>", "Co-Authored-By: OpenAI Codex gpt-6.1-sol <noreply@openai.com>"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%q, %v", got, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "coauthors.ndjson"))
	if err != nil || strings.Count(string(data), "\n") != 5 {
		t.Fatalf("records: %s, %v", data, err)
	}
	cfg.Enabled = false
	if got, err := Lines(dir, cfg, time.Time{}); err != nil || len(got) != 0 {
		t.Fatalf("disabled: %q %v", got, err)
	}
}

func TestConcurrentPeerAndWorkerGuards(t *testing.T) {
	t.Setenv("DUOLOG_DEPTH", "0")
	dir := t.TempDir()
	cfg := settings()
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if err := Append(dir, cfg, Record{Peer: "peer-once", Agent: "codex", Model: "gpt-6.1-sol", Files: 1}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	data, err := os.ReadFile(filepath.Join(dir, "coauthors.ndjson"))
	if err != nil || strings.Count(string(data), "\n") != 1 {
		t.Fatalf("%s %v", data, err)
	}
	for _, depth := range []string{"1", "2"} {
		t.Setenv("DUOLOG_DEPTH", depth)
		if err := Append(dir, cfg, Record{Agent: "claude", Model: "claude-opus-5-5", Files: 1}); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := os.ReadFile(filepath.Join(dir, "coauthors.ndjson"))
	if string(after) != string(data) {
		t.Fatal("worker wrote records")
	}
}

func TestHookMessage(t *testing.T) {
	t.Setenv("DUOLOG_DEPTH", "0")
	line := "Co-Authored-By: OpenAI Codex gpt-6.1-sol <noreply@openai.com>"
	for _, tc := range []struct{ message, want string }{
		{"subject", "subject\n\n" + line + "\n"},
		{"fix: example\n", "fix: example\n\n" + line + "\n"},
		{"subject\n\nbody\nExample: text\n", "subject\n\nbody\nExample: text\n\n" + line + "\n"},
		{"subject\r\n\r\nbody\r\n", "subject\r\n\r\nbody\r\n\r\n" + line + "\r\n"},
		{"subject\n\nbody\n", "subject\n\nbody\n\n" + line + "\n"},
		{"subject\n\nSigned-off-by: Example <example@example.com>\n", "subject\n\nSigned-off-by: Example <example@example.com>\n" + line + "\n"},
		{"subject\n\n" + strings.ToLower(line) + "\n", "subject\n\n" + strings.ToLower(line) + "\n"},
		{"", line + "\n"},
	} {
		path := filepath.Join(t.TempDir(), "message")
		if err := os.WriteFile(path, []byte(tc.message), 0600); err != nil {
			t.Fatal(err)
		}
		if err := Hook(path, []string{line}); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(path)
		if string(got) != tc.want {
			t.Errorf("message %q: %q, want %q", tc.message, got, tc.want)
		}
	}
}
