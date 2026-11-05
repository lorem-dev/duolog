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
	// works today: an explicit env_passthrough entry. An explicit list
	// replaces the defaults, so the line shows the whole list to write: the
	// names in force now plus the key.
	names := []string{"PATH", "HOME", "USER", "CODEX_HOME", "CLAUDE_CONFIG_DIR"}
	if got, want := MigrationLine("claude", "ANTHROPIC_API_KEY", names), "claude workers no longer get ANTHROPIC_API_KEY by default. To let them use it, keep the default names and add the key, since an explicit list replaces the defaults: env_passthrough: [PATH, HOME, USER, CODEX_HOME, CLAUDE_CONFIG_DIR, ANTHROPIC_API_KEY]"; got != want {
		t.Errorf("MigrationLine = %q, want %q", got, want)
	}
	if got, want := MigrationLine("codex", "OPENAI_API_KEY", names), "codex workers no longer get OPENAI_API_KEY by default. To let them use it, keep the default names and add the key, since an explicit list replaces the defaults: env_passthrough: [PATH, HOME, USER, CODEX_HOME, CLAUDE_CONFIG_DIR, OPENAI_API_KEY]"; got != want {
		t.Errorf("MigrationLine = %q, want %q", got, want)
	}
	// The caller's list stays as it was.
	if len(names) != 5 || cap(names) != 5 {
		t.Errorf("MigrationLine changed its names: %v", names)
	}
	for _, agent := range []string{"claude", "codex"} {
		if line := MigrationLine(agent, "KEY", names); strings.Contains(line, "paid_runs") || strings.Contains(line, "explicitly.") {
			t.Errorf("MigrationLine advertises unsupported or incomplete config: %q", line)
		}
	}
}

// Calibration runs on the subscription only, so it needs to know which
// billing keys env_passthrough hands to workers, whichever agent they belong
// to.
func TestListedKeys(t *testing.T) {
	for _, c := range []struct {
		name  string
		names []string
		want  []string
	}{
		{"defaults", []string{"PATH", "HOME", "USER", "CODEX_HOME", "CLAUDE_CONFIG_DIR"}, nil},
		{"none", nil, nil},
		{"claude key", []string{"PATH", "ANTHROPIC_API_KEY"}, []string{"ANTHROPIC_API_KEY"}},
		{"both keys", []string{"OPENAI_API_KEY", "HOME", "ANTHROPIC_API_KEY"}, []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY"}},
		{"similar names", []string{"ANTHROPIC_API_KEY_OLD", "XOPENAI_API_KEY"}, nil},
	} {
		if got := ListedKeys(c.names); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: ListedKeys = %v, want %v", c.name, got, c.want)
		}
	}
}
