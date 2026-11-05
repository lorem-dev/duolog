package workerenv

import (
	"reflect"
	"strings"
	"testing"
)

// A billing key counts as dropped when duolog's own environment sets it, even
// empty, and env_passthrough does not list it. A listed key passes to workers,
// so the person followed the advice and hears no more of it.
func TestDroppedKeysSetNotListed(t *testing.T) {
	claude := []string{"ANTHROPIC_API_KEY"}
	codex := []string{"OPENAI_API_KEY"}
	for _, c := range []struct {
		name           string
		names, environ []string
		want           map[string][]string
	}{
		{"neither", []string{"PATH", "HOME"}, []string{"PATH=/bin", "HOME=/home"}, map[string][]string{}},
		{"set", nil, []string{"ANTHROPIC_API_KEY=sk-test-value"}, map[string][]string{"claude": claude}},
		{"set empty", nil, []string{"OPENAI_API_KEY="}, map[string][]string{"codex": codex}},
		{"listed", []string{"PATH", "ANTHROPIC_API_KEY"}, []string{"PATH=/bin"}, map[string][]string{}},
		{"listed and set", []string{"OPENAI_API_KEY"}, []string{"OPENAI_API_KEY=sk-test-value", "ANTHROPIC_API_KEY="}, map[string][]string{"claude": claude}},
		{"similar names", []string{"ANTHROPIC_API_KEY_OLD"}, []string{"XOPENAI_API_KEY=x", "ANTHROPIC_API_KEYS=x"}, map[string][]string{}},
		{"similar listed name", []string{"ANTHROPIC_API_KEY_OLD"}, []string{"ANTHROPIC_API_KEY=x"}, map[string][]string{"claude": claude}},
	} {
		if got := DroppedKeys(c.names, c.environ); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: DroppedKeys = %v, want %v", c.name, got, c.want)
		}
	}

	// The config has no paid_runs key yet, so the line names the way that
	// works today: an explicit env_passthrough entry.
	if got, want := MigrationLine("claude", "ANTHROPIC_API_KEY"), "claude workers no longer get ANTHROPIC_API_KEY by default. To let them use it, list it in env_passthrough explicitly."; got != want {
		t.Errorf("MigrationLine = %q, want %q", got, want)
	}
	if got, want := MigrationLine("codex", "OPENAI_API_KEY"), "codex workers no longer get OPENAI_API_KEY by default. To let them use it, list it in env_passthrough explicitly."; got != want {
		t.Errorf("MigrationLine = %q, want %q", got, want)
	}
	for _, agent := range []string{"claude", "codex"} {
		if line := MigrationLine(agent, "KEY"); strings.Contains(line, "paid_runs") {
			t.Errorf("MigrationLine advertises unsupported config: %q", line)
		}
	}
}
