package wiring

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
)

func TestSweepLine(t *testing.T) {
	for _, tc := range []struct {
		lines []string
		want  string
	}{
		{nil, ""},
		{[]string{"one"}, "duolog: one"},
		{[]string{"one", "two", "three"}, "duolog: one; and 2 more changes to Codex trust"},
	} {
		if got := SweepLine(tc.lines); got != tc.want {
			t.Errorf("SweepLine(%q) = %q, want %q", tc.lines, got, tc.want)
		}
	}
}

// The sweep runs only for an agent the config enables: with Codex switched
// off, a removed worktree's tables stay in its config.
func TestSweepWorktreeTrustNeedsCodexEnabled(t *testing.T) {
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	source := t.TempDir()
	gone := filepath.Join(source, ".worktrees", "gone")
	// The folder above a removed worktree exists; only then does the sweep
	// take its tables out.
	if err := os.MkdirAll(filepath.Dir(gone), 0o755); err != nil {
		t.Fatal(err)
	}
	approval := func(root string) string { return filepath.Join(root, ".codex", "hooks.json") + ":stop:0:0" }
	text := fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"sha256:x\"\n\n", approval(source)) +
		fmt.Sprintf("# duolog: %s is a linked worktree of %s, which Codex trusts with duolog's hooks approved.\n", gone, source) +
		fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"sha256:x\"\n# duolog: end\n", approval(gone))
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	environ := []string{"HOME=" + home, "USERPROFILE=" + home, "CODEX_HOME=" + codexHome}
	off, on := false, true
	disabled := &config.Config{Agents: map[string]config.AgentSwitch{"claude": {Enabled: &on}, "codex": {Enabled: &off}}}
	if lines, err := SweepWorktreeTrust(t.TempDir(), disabled, environ); err != nil || len(lines) != 0 {
		t.Fatalf("disabled codex swept: %q, %v", lines, err)
	}
	if lines, err := SweepWorktreeTrust(t.TempDir(), nil, environ); err != nil || len(lines) != 0 {
		t.Fatalf("no config swept: %q, %v", lines, err)
	}
	if data, err := os.ReadFile(filepath.Join(codexHome, "config.toml")); err != nil || string(data) != text {
		t.Fatalf("config changed: %q %v", data, err)
	}
	enabled := &config.Config{Agents: map[string]config.AgentSwitch{"codex": {Enabled: &on}}}
	if lines, err := SweepWorktreeTrust(t.TempDir(), enabled, environ); err != nil || len(lines) != 1 {
		t.Fatalf("enabled sweep = %q, %v", lines, err)
	}
	want := fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"sha256:x\"\n", approval(source))
	if data, err := os.ReadFile(filepath.Join(codexHome, "config.toml")); err != nil || string(data) != want {
		t.Fatalf("pruned config: %q %v", data, err)
	}
}
