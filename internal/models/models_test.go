package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
)

// The copied test executable handles metadata commands without starting an agent.
func TestMain(m *testing.M) {
	if os.Getenv("DUOLOG_FAKE_CODEX") == "1" {
		input, _ := io.ReadAll(os.Stdin)
		if len(input) != 0 {
			os.Exit(2)
		}
		args := strings.Join(os.Args[1:], " ")
		if args == "--version" {
			fmt.Println(os.Getenv("DUOLOG_FAKE_VERSION"))
			os.Exit(0)
		}
		if args == "debug models" {
			if os.Getenv("DUOLOG_FAKE_FAIL") == "1" {
				os.Exit(1)
			}
			if err := os.WriteFile(os.Getenv("DUOLOG_FAKE_CALLED"), []byte("called"), 0600); err != nil {
				os.Exit(2)
			}
			data, err := os.ReadFile(os.Getenv("DUOLOG_FAKE_FIXTURE"))
			if err != nil {
				os.Exit(2)
			}
			os.Stdout.Write(data)
			os.Exit(0)
		}
		os.Exit(2)
	}
	os.Exit(m.Run())
}

func fixture(t *testing.T) []Entry {
	t.Helper()
	data, err := os.ReadFile("../../scripts/bench/fixtures/models/codex-debug-models.trimmed.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Models []Entry `json:"models"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	return catalog.Models
}
func modelConfig() *config.Config {
	return &config.Config{Models: map[string]config.Model{"sol": {Agent: "codex", Family: "sol"}}}
}
func TestLatestPicksHighestVersion(t *testing.T) {
	if got, ok := Latest("sol", fixture(t)); !ok || got != "gpt-6.1-sol" {
		t.Fatalf("got %q, %v", got, ok)
	}
	list := []Entry{{Slug: "gpt-6.9-sol", Visibility: "list"}, {Slug: "gpt-6.10-sol", Visibility: "list"}}
	if got, ok := Latest("sol", list); !ok || got != "gpt-6.10-sol" {
		t.Fatalf("got %q, %v", got, ok)
	}
}
func TestLatestSkipsHidden(t *testing.T) {
	list := append(fixture(t), Entry{Slug: "gpt-99-sol", Visibility: "hide"})
	if got, ok := Latest("sol", list); !ok || got != "gpt-6.1-sol" {
		t.Fatalf("got %q, %v", got, ok)
	}
}
func TestLatestUnknownFamily(t *testing.T) {
	if got, ok := Latest("unknown", fixture(t)); ok || got != "" {
		t.Fatalf("got %q, %v", got, ok)
	}
}
func TestResolveClaudeAliasUnchanged(t *testing.T) {
	r := Resolver{List: func(context.Context) ([]Entry, string, error) { t.Fatal("list called"); return nil, "", nil }}
	for _, name := range []string{"opus", "claude-opus-5-5"} {
		got, err := r.Resolve(context.Background(), modelConfig(), config.Target{Agent: "claude", Model: name})
		if err != nil || got != name {
			t.Fatalf("got %q, %v", got, err)
		}
	}
}
func TestResolvePinnedUnchanged(t *testing.T) {
	r := Resolver{List: func(context.Context) ([]Entry, string, error) { t.Fatal("list called"); return nil, "", nil }}
	got, err := r.Resolve(context.Background(), modelConfig(), config.Target{Agent: "codex", Model: "gpt-6-sol"})
	if err != nil || got != "gpt-6-sol" {
		t.Fatalf("got %q, %v", got, err)
	}
}
func fakeCodex(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	name := "codex"
	if os.PathSeparator == '\\' {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0700); err != nil {
		t.Fatal(err)
	}
	path, err := filepath.Abs("../../scripts/bench/fixtures/models/codex-debug-models.trimmed.json")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "called")
	t.Setenv("PATH", dir)
	t.Setenv("DUOLOG_FAKE_CODEX", "1")
	t.Setenv("DUOLOG_FAKE_FIXTURE", path)
	t.Setenv("DUOLOG_FAKE_CALLED", marker)
	t.Setenv("DUOLOG_FAKE_VERSION", "codex-cli test-1")
	t.Setenv("DUOLOG_FAKE_FAIL", "0")
	return marker
}
func resolveSol(t *testing.T, r Resolver) string {
	t.Helper()
	got, err := r.Resolve(context.Background(), modelConfig(), config.Target{Agent: "codex", Model: "sol"})
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func TestResolveUsesCache(t *testing.T) {
	marker := fakeCodex(t)
	dir := t.TempDir()
	if got, err := Resolve(context.Background(), modelConfig(), dir, config.Target{Agent: "codex", Model: "sol"}); err != nil || got != "gpt-6.1-sol" {
		t.Fatalf("got %s, %v", got, err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if got := resolveSol(t, Resolver{CacheDir: dir}); got != "gpt-6.1-sol" {
		t.Fatal(got)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("debug models ran with a fresh cache")
	}
}
func TestCacheRefreshesOnCodexVersion(t *testing.T) {
	marker := fakeCodex(t)
	r := Resolver{CacheDir: t.TempDir()}
	resolveSol(t, r)
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DUOLOG_FAKE_VERSION", "codex-cli test-2")
	resolveSol(t, r)
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("catalog was not refreshed:", err)
	}
}
func TestCacheRefreshesAfter24Hours(t *testing.T) {
	marker := fakeCodex(t)
	r := Resolver{CacheDir: t.TempDir()}
	resolveSol(t, r)
	path := filepath.Join(r.CacheDir, "models.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc["updated_at"] = time.Now().Add(-25 * time.Hour).UTC().Format(time.RFC3339Nano)
	data, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	resolveSol(t, r)
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("expired catalog was not refreshed:", err)
	}
}
func TestCacheUsedWhenCommandFails(t *testing.T) {
	list := fixture(t)
	dir := t.TempDir()
	r := Resolver{CacheDir: dir, List: func(context.Context) ([]Entry, string, error) { return list, "v1", nil }}
	resolveSol(t, r)
	r.List = func(context.Context) ([]Entry, string, error) { return nil, "", errors.New("offline") }
	if got := resolveSol(t, r); got != "gpt-6.1-sol" {
		t.Fatal(got)
	}
}
func TestNoListAndNoCacheIsError(t *testing.T) {
	r := Resolver{CacheDir: t.TempDir(), List: func(context.Context) ([]Entry, string, error) { return nil, "", errors.New("offline") }}
	if _, err := r.Resolve(context.Background(), modelConfig(), config.Target{Agent: "codex", Model: "sol"}); err == nil {
		t.Fatal("missing error")
	}
}
func TestNormalizeTable(t *testing.T) {
	for _, tc := range []struct{ agent, concrete, want string }{
		{"claude", "claude-opus-5-5", "opus"}, {"claude", "claude-sonnet-5-5", "sonnet"}, {"claude", "claude-haiku-5-5", "haiku"}, {"codex", "gpt-6.1-terra", "terra"}, {"codex", "gpt-6-sol", "sol"}, {"claude", "unknown", "unknown"}, {"codex", "unknown", "unknown"}, {"claude", "opus", "opus"}, {"claude", "claude-future-family-6-1", "future-family"},
	} {
		if got := Normalize(tc.agent, tc.concrete); got != tc.want {
			t.Errorf("Normalize(%q,%q)=%q, want %q", tc.agent, tc.concrete, got, tc.want)
		}
	}
}
