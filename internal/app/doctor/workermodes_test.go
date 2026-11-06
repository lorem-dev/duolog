package doctor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/install"
)

func TestDoctorReportsWorkerModes(t *testing.T) {
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(k, home)
	}
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	root := t.TempDir()
	d := testDeps
	var gateCalls int
	d.GateBinary = func(string) (string, error) { gateCalls++; return "", errors.New("duolog is not on the worker PATH") }
	cfg := &config.Config{EnvPassthrough: config.DefaultEnvPassthrough(runtime.GOOS), WorkerModes: config.WorkerModes{Roles: map[string]config.WorkerMode{"implement": {Claude: "auto"}, "docs": {Claude: "plan"}}}}
	got := workerModeFindings(d, cfg, root)
	want := []Finding{
		{"info", "worker_modes: role docs asks claude plan; it applies always"},
		{"info", "worker_modes: role implement asks claude auto; it applies when the lead is in auto or full"},
		{"warning", "worker_modes: role implement asks claude auto, but the gate cannot be shown to run for claude here: Claude has no duolog gate here; delegate refuses such a run"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	if gateCalls != 0 {
		t.Fatalf("GateBinary called %d times before the hooks were shown", gateCalls)
	}
}

// TestDoctorUsesGateBinary: with the Claude gate in the worker's settings, the
// GateBinary of Deps is called once, and its error is the worker-mode advice.
func TestDoctorUsesGateBinary(t *testing.T) {
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(k, home)
	}
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	gate := `{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"duolog gate --agent claude --event PreToolUse"}]}]}}`
	if err := os.WriteFile(settings, []byte(gate), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Roles:          map[string]config.Role{"implement": {Sandbox: "workspace-write"}},
		EnvPassthrough: []string{"HOME", "USERPROFILE", "USER"},
		WorkerModes:    config.WorkerModes{Roles: map[string]config.WorkerMode{"implement": {Claude: "bypassPermissions"}}},
	}
	calls := 0
	d := testDeps
	d.GateBinary = func(string) (string, error) {
		calls++
		return "", errors.New("duolog is not on the worker PATH")
	}
	got := findingText(workerModeFindings(d, cfg, t.TempDir()))
	if calls != 1 || !strings.Contains(got, "warning: worker_modes: role implement asks claude bypassPermissions") || !strings.Contains(got, "duolog is not on the worker PATH") {
		t.Fatalf("GateBinary called %d times:\n%s", calls, got)
	}
}

// Claude review M1: an entry on a read-only role never applies above plan; a
// family entry only lowers. Claude review I3: extras alone are a
// workspace-write request, as delegate applies them.
func TestDoctorSaysWhenAnEntryNeverApplies(t *testing.T) {
	cfg := &config.Config{
		EnvPassthrough: config.DefaultEnvPassthrough(runtime.GOOS),
		Roles:          map[string]config.Role{"review_final": {Sandbox: "read-only"}, "implement": {Sandbox: "workspace-write"}},
		WorkerModes: config.WorkerModes{
			Roles:    map[string]config.WorkerMode{"review_final": {Claude: "bypassPermissions", Codex: "read-only"}, "implement": {Network: true}},
			Families: map[string]config.WorkerMode{"haiku": {Claude: "bypassPermissions"}},
		},
	}
	d := testDeps
	d.GateBinary = func(string) (string, error) { return "/bin/duolog", nil }
	got := workerModeFindings(d, cfg, t.TempDir())
	var lines []string
	for _, f := range got {
		lines = append(lines, f.Level+": "+f.Message)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"info: worker_modes: role review_final asks claude bypassPermissions; it never applies: role review_final is read-only, so its workers start at level plan",
		"info: worker_modes: role review_final asks codex read-only; it applies always",
		"info: worker_modes: role implement asks codex workspace-write with network; it applies when the lead is in edits or full",
		"info: worker_modes: family haiku asks claude bypassPermissions; a family entry only lowers: it lowers a role entry above it, and alone it changes nothing",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("lacks %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "warning: worker_modes: family") || strings.Contains(joined, "warning: worker_modes: role review_final") {
		t.Errorf("warns about an entry that cannot loosen:\n%s", joined)
	}
}

// Re-review, Minor 1: doctor judges the gate from the environment a worker
// gets, as delegate does. The review's input: the gate, or the Codex folder
// trust, only in the caller's CLAUDE_CONFIG_DIR or CODEX_HOME, and
// env_passthrough without them. Passed on, the same folder counts.
func TestDoctorReadsTheWorkersSettings(t *testing.T) {
	gate := `{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"duolog gate --agent claude --event PreToolUse"}]}]}}`
	write := func(t *testing.T, path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, agent, want string
		entry             config.WorkerMode
		setup             func(t *testing.T, home, root, alternate string)
	}{
		{"claude gate only in the caller's CLAUDE_CONFIG_DIR", "claude", "Claude has no duolog gate here", config.WorkerMode{Claude: "bypassPermissions"},
			func(t *testing.T, _, _, alternate string) {
				write(t, filepath.Join(alternate, "settings.json"), gate)
				t.Setenv("CLAUDE_CONFIG_DIR", alternate)
			}},
		{"codex folder trust only in the caller's CODEX_HOME", "codex", "Codex does not trust this folder", config.WorkerMode{Codex: "workspace-write", Network: true},
			func(t *testing.T, home, root, alternate string) {
				hooks, err := install.HooksJSON("codex", 30)
				if err != nil {
					t.Fatal(err)
				}
				write(t, filepath.Join(root, ".codex", "hooks.json"), string(hooks))
				approval := fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"fixture-hash\"\n", filepath.Join(root, ".codex", "hooks.json")+":PreToolUse:0:0")
				write(t, filepath.Join(home, ".codex", "config.toml"), approval)
				write(t, filepath.Join(alternate, "config.toml"), approval+fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", root))
				t.Setenv("CODEX_HOME", alternate)
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA"} {
				t.Setenv(k, home)
			}
			t.Setenv("CODEX_HOME", "")
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			root, alternate := t.TempDir(), t.TempDir()
			tc.setup(t, home, root, alternate)
			d := testDeps
			d.GateBinary = func(string) (string, error) { return "/bin/duolog", nil }
			cfg := &config.Config{
				Roles:          map[string]config.Role{"implement": {Sandbox: "workspace-write"}},
				EnvPassthrough: []string{"HOME", "USERPROFILE", "USER"},
				WorkerModes:    config.WorkerModes{Roles: map[string]config.WorkerMode{"implement": tc.entry}},
			}
			if joined := findingText(workerModeFindings(d, cfg, root)); !strings.Contains(joined, "warning: ") || !strings.Contains(joined, tc.want) {
				t.Errorf("no warning %q:\n%s", tc.want, joined)
			}
			cfg.EnvPassthrough = append(cfg.EnvPassthrough, "CLAUDE_CONFIG_DIR", "CODEX_HOME")
			if joined := findingText(workerModeFindings(d, cfg, root)); strings.Contains(joined, "warning: ") {
				t.Errorf("warns with the folder passed on:\n%s", joined)
			}
		})
	}
}

func findingText(findings []Finding) string {
	var lines []string
	for _, f := range findings {
		lines = append(lines, f.Level+": "+f.Message)
	}
	return strings.Join(lines, "\n")
}

func TestDoctorReportsIgnoredWorkerModes(t *testing.T) {
	cfg := &config.Config{WorkerModes: config.WorkerModes{Ignored: []string{"worker_modes.roles.implement.claude bypassPermissions in .duolog/config.yaml is ignored: x"}}}
	got := workerModeFindings(testDeps, cfg, t.TempDir())
	if len(got) != 1 || got[0].Level != "warning" || !strings.HasPrefix(got[0].Message, "worker_modes.roles.implement.claude bypassPermissions") {
		t.Fatal(got)
	}
}
