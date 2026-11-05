package workerenv

import (
	"runtime"
	"strings"
)

// droppedKeys are the billing keys, per agent, that workers and peers no
// longer inherit: a worker with one is billed per token.
var droppedKeys = map[string][]string{
	"claude": {"ANTHROPIC_API_KEY"},
	"codex":  {"OPENAI_API_KEY"},
}

// DroppedKeys lists, per agent, the dropped billing keys that the person
// still uses: set in duolog's own environment, even empty, and not listed in
// names, the env_passthrough list. A listed key passes to workers, so it is
// not dropped. It compares names only, never values.
func DroppedKeys(names, environ []string) map[string][]string {
	out := map[string][]string{}
	for _, agent := range []string{"claude", "codex"} {
		for _, key := range droppedKeys[agent] {
			if set(environ, key) && !listed(names, key) {
				out[agent] = append(out[agent], key)
			}
		}
	}
	return out
}

// MigrationLine tells the lead that an agent's workers no longer get name by
// default. It names an explicit env_passthrough entry, which passes the key
// today; the config has no paid_runs key yet.
func MigrationLine(agent, name string) string {
	return agent + " workers no longer get " + name + " by default. To let them use it, list it in env_passthrough explicitly."
}

func listed(names []string, key string) bool {
	for _, name := range names {
		if sameName(name, key) {
			return true
		}
	}
	return false
}

func set(environ []string, key string) bool {
	for _, pair := range environ {
		if name, _, _ := strings.Cut(pair, "="); sameName(name, key) {
			return true
		}
	}
	return false
}

// sameName compares variable names as the platform does: Windows ignores case.
func sameName(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
