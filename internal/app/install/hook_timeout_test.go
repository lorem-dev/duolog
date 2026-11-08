package install

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestInstallUpdatesAnOldGateTimeout(t *testing.T) {
	home, root := setup(t)
	o := Options{Scope: "project", Agent: "both", Home: home, Root: root}
	run(t, o)
	current := fmt.Sprintf(`"timeout": %d`, GateTimeout)
	files := []string{filepath.Join(root, ".claude", "settings.json"), filepath.Join(root, ".codex", "hooks.json")}
	for _, path := range files {
		text := read(t, path)
		if !strings.Contains(text, current) {
			t.Fatalf("%s has no %s:\n%s", path, current, text)
		}
		write(t, path, strings.ReplaceAll(text, current, `"timeout": 150`))
	}
	statuses, err := testDeps().Check(o)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range statuses {
		if !s.Stale() || !slices.Contains(s.Changed, s.HookFile) {
			t.Errorf("%s: install --check missed the old timeout: %+v", s.Agent, s)
		}
	}
	run(t, o)
	for _, path := range files {
		if text := read(t, path); strings.Contains(text, `"timeout": 150`) || !strings.Contains(text, current) {
			t.Errorf("%s was not updated:\n%s", path, text)
		}
	}
	statuses, err = testDeps().Check(o)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range statuses {
		if slices.Contains(s.Changed, s.HookFile) {
			t.Errorf("%s: still stale after install: %+v", s.Agent, s)
		}
	}
}

// Codex hashes the timeout into a hook's approval and skips the hook once the
// hash differs, so the approval it holds must stop counting.
func TestNewTimeoutMakesTheCodexApprovalStale(t *testing.T) {
	home, root := setup(t)
	o := Options{Scope: "project", Agent: "codex", Home: home, Root: root}
	run(t, o)
	path := filepath.Join(root, ".codex", "hooks.json")
	write(t, path, strings.ReplaceAll(read(t, path), fmt.Sprintf(`"timeout": %d`, GateTimeout), `"timeout": 150`))
	key := path + ":pre_tool_use:0:0"
	write(t, filepath.Join(home, ".codex", "config.toml"), fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"approved\"\n", key))
	run(t, o)
	if stale := read(t, filepath.Join(home, ".codex", "duolog-stale-approvals.json")); !strings.Contains(stale, "pre_tool_use:0:0") || !strings.Contains(stale, "approved") {
		t.Fatalf("approval not marked stale: %s", stale)
	}
}
