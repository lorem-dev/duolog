package hook

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/load"
)

// growTo appends a Claude turn to the lead's transcript whose context is n tokens.
func growTo(t *testing.T, e Env, n int) {
	t.Helper()
	var h input
	if err := json.Unmarshal(e.Input, &h); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(h.TranscriptPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	line := fmt.Sprintf(`{"type":"assistant","message":{"model":"m","usage":{"input_tokens":5,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,"output_tokens":3}}}`+"\n", n-105, 100)
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
}

func prompt(t *testing.T, e Env) string {
	t.Helper()
	return string(Prompt(context.Background(), e).Stdout)
}

const hintStart = "Context is about "

func idleLoad(e Env, running int) Env {
	e.Load = func(context.Context, string) load.Summary {
		return load.Summary{Agents: []load.Agent{{Name: "claude", Running: running, Limit: 12, Live: running}}}
	}
	return e
}

func TestPromptOffersAFreshSessionOnceAndAgainEvery100kTokens(t *testing.T) {
	e, _ := registered(t, "claude")
	e = idleLoad(e, 0)
	growTo(t, e, 150000)
	if got := prompt(t, e); strings.Contains(got, hintStart) {
		t.Fatalf("below the limit: %q", got)
	}
	growTo(t, e, 212345)
	got := prompt(t, e)
	if !strings.Contains(got, "Context is about 212k tokens.") {
		t.Fatalf("got %q, want the size", got)
	}
	// A lead in bypass mode sat for hours on the offer: it must keep working.
	for _, part := range []string{"do not stop or wait for an answer", "go on with the next card", "not a question"} {
		if !strings.Contains(got, part) {
			t.Fatalf("got %q, want %q", got, part)
		}
	}
	if strings.Contains(got, "without their answer") {
		t.Fatalf("the hint makes the lead wait: %q", got)
	}
	if got := prompt(t, e); strings.Contains(got, hintStart) {
		t.Fatalf("said twice: %q", got)
	}
	growTo(t, e, 305000)
	if got := prompt(t, e); strings.Contains(got, hintStart) {
		t.Fatalf("less than 100k further: %q", got)
	}
	growTo(t, e, 312345)
	if got := prompt(t, e); !strings.Contains(got, "Context is about 312k tokens.") {
		t.Fatalf("100k further: %q", got)
	}
	if got := prompt(t, e); strings.Contains(got, hintStart) {
		t.Fatalf("said twice at 312k: %q", got)
	}
	// The load line keeps its own record beside the hint.
	if got := prompt(t, e); strings.Contains(got, hintStart) {
		t.Fatalf("said again: %q", got)
	}
}

func TestFreshSessionHintWaitsForIdlePeers(t *testing.T) {
	e, _ := registered(t, "claude")
	growTo(t, e, 250000)
	e = idleLoad(e, 2)
	if got := prompt(t, e); strings.Contains(got, hintStart) {
		t.Fatalf("with peers running: %q", got)
	}
	e = idleLoad(e, 0)
	if got := prompt(t, e); !strings.Contains(got, hintStart) {
		t.Fatalf("peers gone: %q", got)
	}
}

func TestFreshSessionHintWaitsForAForegroundWorker(t *testing.T) {
	e, reg, _ := setup(t, "claude")
	SessionStart(context.Background(), e)
	growTo(t, e, 250000)
	if err := reg.SetWaiting(id(e), "run1", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if got := prompt(t, e); strings.Contains(got, hintStart) {
		t.Fatalf("with a worker running: %q", got)
	}
	if err := reg.SetWaiting(id(e), "run1", 0); err != nil {
		t.Fatal(err)
	}
	if got := prompt(t, e); !strings.Contains(got, hintStart) {
		t.Fatalf("worker done: %q", got)
	}
}

func TestFreshSessionHintCanBeOffOrUnreadable(t *testing.T) {
	e, _ := registered(t, "claude")
	growTo(t, e, 250000)
	path := filepath.Join(userConfigDir(t), "duolog", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("lead: {fresh_session_hint: 0}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := prompt(t, e); strings.Contains(got, hintStart) {
		t.Fatalf("switched off: %q", got)
	}
	if err := os.WriteFile(path, []byte("lead: {fresh_session_hint: 300000}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := prompt(t, e); strings.Contains(got, hintStart) {
		t.Fatalf("below a raised limit: %q", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	// A transcript that is gone adds nothing and fails nothing.
	var h input
	if err := json.Unmarshal(e.Input, &h); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(h.TranscriptPath); err != nil {
		t.Fatal(err)
	}
	if got := prompt(t, e); strings.Contains(got, hintStart) {
		t.Fatalf("no transcript: %q", got)
	}
}

func TestCodexLeadGetsNoFreshSessionHint(t *testing.T) {
	e, _ := registered(t, "codex")
	if got := prompt(t, e); strings.Contains(got, hintStart) {
		t.Fatalf("codex: %q", got)
	}
}

func TestWorkersAndPeersGetNoFreshSessionHint(t *testing.T) {
	e, _ := registered(t, "claude")
	growTo(t, e, 250000)
	for _, extra := range []string{"DUOLOG_DEPTH=1", "DUOLOG_PEER=peer-ab12cd"} {
		other := e
		other.Environ = append(append([]string{}, e.Environ...), extra)
		if out := Prompt(context.Background(), other); strings.Contains(string(out.Stdout), hintStart) {
			t.Fatalf("%s: %q", extra, out.Stdout)
		}
	}
}
