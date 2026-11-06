package route

import (
	"encoding/json"
	"os"
	"testing"
)

func fixture(t *testing.T) []Entry {
	t.Helper()
	data, err := os.ReadFile("../../../scripts/bench/fixtures/models/codex-debug-models.trimmed.json")
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
func TestNormalizeTable(t *testing.T) {
	for _, tc := range []struct{ agent, concrete, want string }{
		{"claude", "claude-opus-5-5", "opus"}, {"claude", "claude-sonnet-5-5", "sonnet"}, {"claude", "claude-haiku-5-5", "haiku"}, {"codex", "gpt-6.1-terra", "terra"}, {"codex", "gpt-6-sol", "sol"}, {"claude", "unknown", "unknown"}, {"codex", "unknown", "unknown"}, {"claude", "opus", "opus"}, {"claude", "claude-future-family-6-1", "future-family"},
	} {
		if got := Normalize(tc.agent, tc.concrete); got != tc.want {
			t.Errorf("Normalize(%q,%q)=%q, want %q", tc.agent, tc.concrete, got, tc.want)
		}
	}
}
