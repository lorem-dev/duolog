package codex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/install"
)

func trustFixture(t *testing.T, body string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if e := os.MkdirAll(filepath.Join(home, ".codex"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
}

// folder and hooks read the process's own environment, as the old
// trust.Folder and trust.Hooks did.
func folder(root string) (bool, error) { return Trust{}.Folder(root, os.Environ()) }

func hooks(root string) (agent.TrustState, error) { return Trust{}.Hooks(root, os.Environ()) }

func writeHook(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, ".codex")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hooks.json"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFolderTrustReportedMissing(t *testing.T) {
	root := t.TempDir()
	trustFixture(t, fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", t.TempDir()))
	ok, e := folder(root)
	if e != nil || ok {
		t.Fatal(ok, e)
	}
	trustFixture(t, fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", root))
	ok, e = folder(root)
	if e != nil || !ok {
		t.Fatal(ok, e)
	}
}

func TestHookTrustMatchesProject(t *testing.T) {
	root := t.TempDir()
	key := filepath.Join(root, ".codex", "hooks.json") + ":PreToolUse:0:0"
	trustFixture(t, fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"invented-hash\"\n", key))
	writeHook(t, root, `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"duolog gate"}]}]}}`)
	s, e := hooks(root)
	if e != nil || !s.Trusted {
		t.Fatal(s, e)
	}
	s, e = hooks(t.TempDir())
	if e != nil || s.Trusted {
		t.Fatal(s, e)
	}
}

// Codex writes approvals with snake_case event names, as in
// "/path/hooks.json:session_start:0:0".
func TestHookTrustReadsCodexEventSpelling(t *testing.T) {
	root := t.TempDir()
	hook := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"duolog gate"}]}]}}`
	path := filepath.Join(root, ".codex", "hooks.json")
	for _, tc := range []struct {
		name, event string
		want        bool
	}{
		{"real spelling", "pre_tool_use", true},
		{"documented spelling", "PreToolUse", true},
		{"other event", "session_start", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := path + ":" + tc.event + ":0:0"
			trustFixture(t, fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"invented-hash\"\n", key))
			writeHook(t, root, hook)
			s, e := hooks(root)
			if e != nil || s.Trusted != tc.want {
				t.Fatal(s, e)
			}
		})
	}
}

func TestMalformedCodexConfigIsRefused(t *testing.T) {
	trustFixture(t, "invalid TOML [")
	if _, e := hooks(t.TempDir()); e == nil {
		t.Fatal("malformed configuration accepted")
	}
}

func TestHookTrustRequiresCurrentGateEntry(t *testing.T) {
	for _, tc := range []struct {
		name, body, entry string
		trusted           bool
	}{
		{"missing file", "", "0:0", false},
		{"deleted gate", `{"hooks":{"PreToolUse":[]}}`, "0:0", false},
		{"changed command", `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"echo duolog gate"}]}]}}`, "0:0", false},
		{"different event", `{"hooks":{"PostToolUse":[{"hooks":[{"type":"command","command":"duolog gate"}]}]}}`, "0:0", false},
		{"wrong entry approved", `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"echo hello"},{"type":"command","command":"duolog gate"}]}]}}`, "0:0", false},
		{"second entry approved", `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"echo hello"},{"type":"command","command":"duolog gate"}]}]}}`, "0:1", true},
		{"different group approved", `{"hooks":{"PreToolUse":[{"hooks":[]},{"hooks":[{"type":"command","command":"duolog gate"}]}]}}`, "1:0", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			key := filepath.Join(root, ".codex", "hooks.json") + ":PreToolUse:" + tc.entry
			trustFixture(t, fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"reviewed-hash\"\n", key))
			if tc.body != "" {
				writeHook(t, root, tc.body)
			}
			got, err := hooks(root)
			if err != nil || got.Trusted != tc.trusted {
				t.Fatalf("trusted=%v want %v, err=%v", got.Trusted, tc.trusted, err)
			}
		})
	}
}

func TestTrustWindowsOverrideOnly(t *testing.T) {
	root := t.TempDir()
	key := filepath.Join(root, ".codex", "hooks.json") + ":PreToolUse:0:0"
	trustFixture(t, fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"fixture\"\n", key))
	writeHook(t, root, `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","commandWindows":"powershell -NoProfile -NonInteractive -Command duolog gate --agent codex"}]}]}}`)
	state, err := hooks(root)
	if err != nil || !state.Installed || !state.Trusted {
		t.Fatal(state, err)
	}
}

// A nonempty approval at the right position says nothing about what runs there.
func TestHookTrustNeedsAGateForEveryTool(t *testing.T) {
	root := t.TempDir()
	key := filepath.Join(root, ".codex", "hooks.json") + ":pre_tool_use:0:0"
	for _, tc := range []struct {
		name, hook string
		want       bool
	}{
		{"gate", `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"duolog gate --agent codex"}]}]}}`, true},
		{"any tool", `{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"duolog gate --agent codex"}]}]}}`, true},
		{"narrow matcher", `{"hooks":{"PreToolUse":[{"matcher":"NoSuchTool","hooks":[{"type":"command","command":"duolog gate --agent codex"}]}]}}`, false},
		{"another subcommand", `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"duolog hook SessionStart --agent codex"}]}]}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trustFixture(t, fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"x\"\n", key))
			writeHook(t, root, tc.hook)
			s, e := hooks(root)
			if e != nil || s.Trusted != tc.want {
				t.Fatal(s, e)
			}
		})
	}
}

// Codex review 6: the preflight refuses a gate it cannot establish now. A
// Codex approval of a hook whose command changed since is not a gate.
func TestGateProblemNeedsTheCurrentGate(t *testing.T) {
	root := t.TempDir()
	trustFixture(t, fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"approved-hash\"\n", filepath.Join(root, ".codex", "hooks.json")+":pre_tool_use:0:0"))
	generated, err := HooksJSON(30)
	if err != nil {
		t.Fatal(err)
	}
	writeHook(t, root, string(generated))
	approveCurrentFixture(t, root)
	if s, err := hooks(root); err != nil || (Trust{}).GateProblem(s, true) != "" {
		t.Fatalf("duolog's own hook: %+v %v %q", s, err, (Trust{}).GateProblem(s, true))
	}
	// Still recognisable as the gate, but not the command that was approved.
	changed := strings.Replace(string(generated), "--event PreToolUse", "--event PreToolUse --agent claude", 2)
	if changed == string(generated) {
		t.Fatal("fixture did not change")
	}
	writeHook(t, root, changed)
	s, err := hooks(root)
	if problem := (Trust{}).GateProblem(s, true); err != nil || !s.Trusted || !strings.Contains(problem, "not duolog's gate command") {
		t.Fatalf("%+v %v %q", s, err, problem)
	}
}

// Re-review Important 3: an approval given for an older duolog's gate command
// does not count after duolog install rewrites the command, until Codex
// records a new hash for it.
func TestStaleCodexApprovalAfterReinstall(t *testing.T) {
	root := t.TempDir()
	key := filepath.Join(root, ".codex", "hooks.json") + ":pre_tool_use:0:0"
	trustFixture(t, fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"old-hash\"\n", key))
	home := os.Getenv("HOME")
	generated, err := HooksJSON(install.GateTimeout)
	if err != nil {
		t.Fatal(err)
	}
	// reinstall is what duolog install does for Codex in this project: merge
	// the hooks, and when the gate command changed, mark the approvals of the
	// old command stale.
	reinstall := func() {
		t.Helper()
		var i Installer
		path := i.HookFile("project", root, home)
		old, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		data, _, err := i.Hooks(old, install.GateTimeout)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, path, string(data))
		if i.GateChanged(old, data) {
			if err := i.MarkStale(home, path); err != nil {
				t.Fatal(err)
			}
		}
	}
	// An unchanged gate keeps its approval through a reinstall.
	writeHook(t, root, string(generated))
	approveCurrentFixture(t, root)
	reinstall()
	if s, err := hooks(root); err != nil || (Trust{}).GateProblem(s, true) != "" {
		t.Fatalf("unchanged gate: %+v %v %q", s, err, (Trust{}).GateProblem(s, true))
	}
	// An older duolog's command, approved, then rewritten by install.
	// Both forms change: on Windows Codex runs and hashes command_windows.
	older := strings.ReplaceAll(string(generated), "duolog gate --agent", "duolog gate --old --agent")
	if older == string(generated) {
		t.Fatal("fixture did not change")
	}
	writeHook(t, root, older)
	approveCurrentFixture(t, root)
	reinstall()
	if data, _ := os.ReadFile(filepath.Join(root, ".codex", "hooks.json")); strings.Contains(string(data), "--old") {
		t.Fatal("install kept the older command")
	}
	s, err := hooks(root)
	if problem := (Trust{}).GateProblem(s, true); err != nil || !strings.Contains(problem, "approve it again in /hooks") {
		t.Fatalf("stale approval: %+v %v %q", s, err, problem)
	}
	// Approving it again in /hooks gives a new hash.
	approveCurrentFixture(t, root)
	if s, err := hooks(root); err != nil || (Trust{}).GateProblem(s, true) != "" {
		t.Fatalf("approved again: %+v %v %q", s, err, (Trust{}).GateProblem(s, true))
	}
}

func TestGateProblem(t *testing.T) {
	for _, tc := range []struct {
		s      agent.TrustState
		folder bool
		want   string
	}{
		{agent.TrustState{Trusted: true}, true, ""},
		{agent.TrustState{}, true, "Codex hooks are not approved; open /hooks"},
		{agent.TrustState{Trusted: true}, false, "Codex does not trust this folder"},
	} {
		if got := (Trust{}).GateProblem(tc.s, tc.folder); got != tc.want {
			t.Errorf("%+v %t: %q want %q", tc.s, tc.folder, got, tc.want)
		}
	}
}

func TestGeneratedHooksTrustedAtBothScopesAndCodexHome(t *testing.T) {
	for _, scope := range []string{"project", "user", "codex-home"} {
		t.Run(scope, func(t *testing.T) {
			home, root := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			t.Setenv("USERPROFILE", home)
			t.Setenv("CODEX_HOME", "")
			dir := filepath.Join(root, ".codex")
			if scope == "user" {
				dir = filepath.Join(home, ".codex")
			}
			configDir := filepath.Join(home, ".codex")
			if scope == "codex-home" {
				configDir = t.TempDir()
				dir = configDir
				t.Setenv("CODEX_HOME", configDir)
			}
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			data, err := HooksJSON(150)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "hooks.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(configDir, 0755); err != nil {
				t.Fatal(err)
			}
			text := fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"fixture\"\n[projects.%q]\ntrust_level = \"trusted\"\n", path+":PreToolUse:0:0", root)
			if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := hooks(root)
			if err != nil || !got.Trusted {
				t.Fatal(got, err)
			}
			if ok, err := folder(root); err != nil || !ok {
				t.Fatal(ok, err)
			}
		})
	}
}

// Re-review, Important 1: Hooks and Folder take the home folder from the
// environment they are given, not from duolog's own, and an environment
// without one is an error, since the agent would fall back to a home that
// environment does not name.
func TestEnvReadsTheEnvironmentsHome(t *testing.T) {
	root := t.TempDir()
	key := filepath.Join(root, ".codex", "hooks.json") + ":PreToolUse:0:0"
	// duolog's own home trusts and approves everything.
	trustFixture(t, fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n[hooks.state.%q]\ntrusted_hash = \"invented-hash\"\n", root, key))
	writeHook(t, root, `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"duolog gate"}]}]}}`)
	// The worker's home has none of it.
	other := []string{"HOME=" + t.TempDir(), "USERPROFILE=" + t.TempDir()}
	if runtime.GOOS == "windows" {
		other = []string{"USERPROFILE=" + t.TempDir()}
	}
	if s, err := (Trust{}).Hooks(root, other); err != nil || s.Trusted {
		t.Errorf("codex: %+v, %v; want no approval from the worker's home", s, err)
	}
	if ok, err := (Trust{}).Folder(root, other); err != nil || ok {
		t.Errorf("codex folder: %t, %v; want untrusted in the worker's home", ok, err)
	}
	none := []string{"PATH=" + os.Getenv("PATH")}
	if _, err := (Trust{}).Hooks(root, none); !errors.Is(err, agent.ErrNoHome) {
		t.Errorf("codex hooks without a home: %v, want ErrNoHome", err)
	}
	if _, err := (Trust{}).Folder(root, none); !errors.Is(err, agent.ErrNoHome) {
		t.Errorf("codex folder without a home: %v, want ErrNoHome", err)
	}
}

// approveCurrentFixture models a person's approval in an isolated test home.
func approveCurrentFixture(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, ".codex", "hooks.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := currentHookHash(data, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(os.Getenv("HOME"), ".codex", "config.toml"), fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = %q\n", path+":pre_tool_use:0:0", hash))
}
