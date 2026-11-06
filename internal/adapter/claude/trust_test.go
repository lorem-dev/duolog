package claude

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

func fixture(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
}

// hooks reads the process's own environment, as the old trust.Hooks did.
func hooks(root string) (agent.TrustState, error) { return Trust{}.Hooks(root, os.Environ()) }

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeSkipsTrust(t *testing.T) {
	fixture(t)
	writeFile(t, filepath.Join(os.Getenv("HOME"), ".codex", "config.toml"), "invalid TOML [")
	root := t.TempDir()
	if ok, e := (Trust{}).Folder(root, os.Environ()); e != nil || !ok {
		t.Fatal(ok, e)
	}
	if s, e := hooks(root); e != nil || !s.Trusted {
		t.Fatal(s, e)
	}
}

// disableAllHooks in any layer Claude loads turns every hook off, the gate
// included, whatever a later layer says; HooksOff names that layer.
func TestClaudeHooksOffInAnyLayer(t *testing.T) {
	root := t.TempDir()
	gate := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"duolog gate"}]}]}}`
	for _, tc := range []struct {
		name, user, project, local, want string
	}{
		{"none", "{}", gate, "{}", ""},
		{"user", `{"disableAllHooks":true}`, gate, "{}", "user"},
		{"project", "{}", `{"disableAllHooks":true,"hooks":{}}`, gate, "project"},
		{"local", "{}", gate, `{"disableAllHooks":true}`, "local"},
		{"later layer turns it back", `{"disableAllHooks":true}`, gate, `{"disableAllHooks":false}`, "user"},
		{"false", `{"disableAllHooks":false}`, gate, "{}", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture(t)
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			files := map[string]string{
				"user":    filepath.Join(home, ".claude", "settings.json"),
				"project": filepath.Join(root, ".claude", "settings.json"),
				"local":   filepath.Join(root, ".claude", "settings.local.json"),
			}
			for layer, body := range map[string]string{"user": tc.user, "project": tc.project, "local": tc.local} {
				writeFile(t, files[layer], body)
			}
			s, err := hooks(root)
			if err != nil || !s.Installed || s.HooksOff != files[tc.want] {
				t.Fatalf("Hooks = %+v, %v; want installed, hooks off by %q", s, err, files[tc.want])
			}
		})
	}
}

func TestClaudeInstalledFollowsSettingsOrder(t *testing.T) {
	root := t.TempDir()
	on, off := `{"enabledPlugins":{"duolog@duolog":true}}`, `{"enabledPlugins":{"duolog@duolog":false}}`
	for _, tc := range []struct {
		name, user, project, local string
		want                       bool
	}{
		{"user only", on, "{}", "{}", true},
		{"project disables", on, off, "{}", false},
		{"local enables", "{}", "{}", on, true},
		{"none", "{}", "{}", "{}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture(t)
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(home, ".claude", "settings.json"), tc.user)
			writeFile(t, filepath.Join(root, ".claude", "settings.json"), tc.project)
			writeFile(t, filepath.Join(root, ".claude", "settings.local.json"), tc.local)
			s, err := hooks(root)
			if err != nil || s.Installed != tc.want {
				t.Fatal(s, err)
			}
		})
	}
}

func TestClaudeFolderTrust(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	accepted := func(path string) string {
		return fmt.Sprintf(`{"projects":{%q:{"hasTrustDialogAccepted":true}}}`, path)
	}
	for name, c := range map[string]struct {
		body           string
		trusted, known bool
	}{
		"missing file":    {"", false, false},
		"not json":        {"{nope", false, false},
		"no entry":        {accepted(other), false, true},
		"refused":         {fmt.Sprintf(`{"projects":{%q:{"hasTrustDialogAccepted":false}}}`, root), false, true},
		"accepted":        {accepted(root), true, true},
		"accepted parent": {accepted(filepath.Dir(root)), true, true},
		"no projects":     {`{"numStartups":3}`, false, true},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			if c.body != "" {
				if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(c.body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if trusted, known := ClaudeFolder(home, root); trusted != c.trusted || known != c.known {
				t.Fatal(trusted, known)
			}
		})
	}
}

func TestClaudeFolderTrustHonorsConfigDir(t *testing.T) {
	root := t.TempDir()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	body := fmt.Sprintf(`{"projects":{%q:{"hasTrustDialogAccepted":true}}}`, root)
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if trusted, known := ClaudeFolder(t.TempDir(), root); !trusted || !known {
		t.Fatal(trusted, known)
	}
}

// Re-review, Important 1: Hooks takes the home folder from the environment it
// is given, not from duolog's own, and an environment without one is an
// error, since the agent would fall back to a home that environment does not
// name.
func TestEnvReadsTheEnvironmentsHome(t *testing.T) {
	root := t.TempDir()
	fixture(t)
	gate := `{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"duolog gate --agent claude --event PreToolUse"}]}]}}`
	writeFile(t, filepath.Join(os.Getenv("HOME"), ".claude", "settings.json"), gate)
	if s, _ := hooks(root); !s.Installed {
		t.Fatal("the fixture's own Claude gate is not seen")
	}
	// The worker's home has none of it.
	other := []string{"HOME=" + t.TempDir(), "USERPROFILE=" + t.TempDir()}
	if runtime.GOOS == "windows" {
		other = []string{"USERPROFILE=" + t.TempDir()}
	}
	if s, err := (Trust{}).Hooks(root, other); err != nil || s.Installed {
		t.Errorf("claude: %+v, %v; want no gate from the worker's home", s, err)
	}
	none := []string{"PATH=" + os.Getenv("PATH")}
	if _, err := (Trust{}).Hooks(root, none); !errors.Is(err, agent.ErrNoHome) {
		t.Errorf("claude hooks without a home: %v, want ErrNoHome", err)
	}
}

// Codex review 6: the preflight refuses a gate it cannot establish now. A
// Claude plugin that is enabled but not installed is not a gate.
func TestGateProblemNeedsTheCurrentGate(t *testing.T) {
	root := t.TempDir()
	fixture(t)
	home := os.Getenv("HOME")
	settings := filepath.Join(home, ".claude", "settings.json")
	writeFile(t, settings, `{"enabledPlugins":{"duolog@duolog":true}}`)
	s, err := hooks(root)
	if problem := (Trust{}).GateProblem(s, true); err != nil || !strings.Contains(problem, "enabled but not installed") {
		t.Fatalf("%+v %v %q", s, err, problem)
	}
	plugin := filepath.Join(home, ".claude", "plugins", "cache", "duolog")
	pluginHooks, err := os.ReadFile(filepath.Join("..", "..", "..", "plugin", "claude", "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(plugin, "hooks", "hooks.json"), string(pluginHooks))
	writeFile(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), fmt.Sprintf(`{"plugins":{"duolog@duolog":[{"installPath":%q}]}}`, plugin))
	if s, err = hooks(root); err != nil || (Trust{}).GateProblem(s, true) != "" {
		t.Fatalf("installed plugin: %+v %v %q", s, err, (Trust{}).GateProblem(s, true))
	}
	// Gate hooks in the settings need no plugin.
	writeFile(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), `{"plugins":{}}`)
	writeFile(t, settings, `{"enabledPlugins":{"duolog@duolog":true},"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"duolog gate --agent claude --event PreToolUse"}]}]}}`)
	if s, err = hooks(root); err != nil || (Trust{}).GateProblem(s, true) != "" {
		t.Fatalf("settings gate: %+v %v %q", s, err, (Trust{}).GateProblem(s, true))
	}
}

// Re-review m6: a plugin install scoped to another project runs no hook here.
func TestPluginInstallForAnotherProject(t *testing.T) {
	root := t.TempDir()
	fixture(t)
	home := os.Getenv("HOME")
	writeFile(t, filepath.Join(home, ".claude", "settings.json"), `{"enabledPlugins":{"duolog@duolog":true}}`)
	plugin := filepath.Join(home, ".claude", "plugins", "cache", "duolog")
	pluginHooks, err := os.ReadFile(filepath.Join("..", "..", "..", "plugin", "claude", "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(plugin, "hooks", "hooks.json"), string(pluginHooks))
	installed := filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
	for _, tc := range []struct {
		entry string
		ok    bool
	}{
		{fmt.Sprintf(`{"scope":"project","projectPath":%q,"installPath":%q}`, t.TempDir(), plugin), false},
		{fmt.Sprintf(`{"scope":"local","projectPath":%q,"installPath":%q}`, t.TempDir(), plugin), false},
		{fmt.Sprintf(`{"scope":"project","projectPath":%q,"installPath":%q}`, root, plugin), true},
		{fmt.Sprintf(`{"scope":"user","installPath":%q}`, plugin), true},
		{fmt.Sprintf(`{"installPath":%q}`, plugin), true},
	} {
		writeFile(t, installed, `{"plugins":{"duolog@duolog":[`+tc.entry+`]}}`)
		s, err := hooks(root)
		if problem := (Trust{}).GateProblem(s, true); err != nil || (problem == "") != tc.ok {
			t.Errorf("%s: %+v %v %q", tc.entry, s, err, problem)
		}
	}
}

func TestGateProblem(t *testing.T) {
	for _, tc := range []struct {
		s      agent.TrustState
		folder bool
		want   string
	}{
		{agent.TrustState{Trusted: true, Installed: true}, true, ""},
		{agent.TrustState{Trusted: true, Installed: true, HooksOff: "/x/settings.json"}, true, "/x/settings.json sets disableAllHooks"},
		{agent.TrustState{Trusted: true}, true, "Claude has no duolog gate here"},
	} {
		if got := (Trust{}).GateProblem(tc.s, tc.folder); got != tc.want {
			t.Errorf("%+v %t: %q want %q", tc.s, tc.folder, got, tc.want)
		}
	}
}

func TestClaudeRecognizesGeneratedAndOlderHooks(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	path := filepath.Join(root, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	generated, err := install.HooksJSON("claude", 150)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{generated,
		[]byte(`{"hooks":{"PreToolUse":[{"hooks":[{"command":"if command -v duolog; then exec duolog gate --agent claude; fi"}]}]}}`),
		[]byte(`{"hooks":{"PreToolUse":[{"hooks":[{"command":"if (Get-Command duolog) { duolog gate --agent claude }"}]}]}}`),
	} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		got, err := hooks(root)
		if err != nil || !got.Installed || !got.Trusted {
			t.Fatal(got, err)
		}
	}
}

// managedGate is a settings file whose PreToolUse hook is the gate install
// writes.
const managedGate = `{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"duolog","args":["gate","--agent","claude","--event","PreToolUse"],"timeout":150}]}]}}`

// Re-review 5, N3: Claude's managed settings decide which hooks run. When
// they set allowManagedHooksOnly or disableAllHooks, the gate in the
// project's own settings does not run, so a held launch is refused unless
// the managed layer holds the gate itself. A gate hook there counts as any
// other, and must be the exact command too.
func TestHeldGateReadsManagedSettings(t *testing.T) {
	home, root, policy := t.TempDir(), t.TempDir(), t.TempDir()
	environ := []string{"HOME=" + home, "USERPROFILE=" + home}
	file, dropIn := filepath.Join(policy, "managed-settings.json"), filepath.Join(policy, "managed-settings.d", "10-hooks.json")
	old := managedFiles
	t.Cleanup(func() { managedFiles = old })
	managedFiles = func(string, string, []string) []string { return []string{file, dropIn} }
	writeFile(t, filepath.Join(root, ".claude", "settings.local.json"), managedGate)
	gate := filepath.Join(home, "bin", "duolog")
	check := func(name, managed, drop string, want bool) {
		t.Helper()
		for path, body := range map[string]string{file: managed, dropIn: drop} {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if body != "" {
				writeFile(t, path, body)
			}
		}
		problem := (Trust{}).HeldGateProblem(root, environ, gate)
		if (problem == "") != want {
			t.Errorf("%s: problem %q, want proven %v", name, problem, want)
		}
		if !want && !strings.Contains(problem, "managed") {
			t.Errorf("%s: %q does not name the managed settings", name, problem)
		}
	}
	check("no managed settings", "", "", true)
	check("managed settings without hooks", `{"permissions":{"deny":["WebFetch"]}}`, "", true)
	check("managed hooks only", `{"allowManagedHooksOnly":true}`, "", false)
	check("managed hooks off", `{"disableAllHooks":true}`, "", false)
	check("drop-in sets managed hooks only", "", `{"allowManagedHooksOnly":true}`, false)
	check("managed hooks only, gate in managed", `{"allowManagedHooksOnly":true,`+managedGate[1:], "", true)
	check("managed hooks only, gate in a drop-in", `{"allowManagedHooksOnly":true}`, managedGate, true)
	check("managed gate only names the gate", `{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"true # duolog gate --agent claude --event PreToolUse"}]}]}}`, "", false)
}
