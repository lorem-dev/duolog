package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/plugin"
)

// setup gives each test a temp Home and Root, and points every home-derived
// variable at a third directory so a stray lookup cannot reach the real one.
func setup(t *testing.T) (home, root string) {
	t.Helper()
	trap := t.TempDir()
	t.Setenv("HOME", trap)
	t.Setenv("USERPROFILE", trap)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(trap, "xdg"))
	t.Setenv("APPDATA", filepath.Join(trap, "appdata"))
	return t.TempDir(), t.TempDir()
}

// snapshot maps every file under dir to its bytes.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, readErr := os.ReadFile(path)
		out[path] = string(data)
		return readErr
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func equal(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func run(t *testing.T, o Options) Report {
	t.Helper()
	r, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestProjectScopeWritesProjectFilesOnly(t *testing.T) {
	home, root := setup(t)
	run(t, Options{Scope: "project", Agent: "both", Home: home, Root: root})
	if got := snapshot(t, home); len(got) != 0 {
		t.Fatalf("home changed: %v", got)
	}
	for _, rel := range []string{".claude/settings.json", ".claude/skills/duolog/SKILL.md", ".codex/hooks.json", ".agents/skills/duolog/SKILL.md", ".duolog/config.yaml"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
}

func TestUserScopeWritesHomeFilesOnly(t *testing.T) {
	home, root := setup(t)
	run(t, Options{Scope: "user", Agent: "both", Home: home, Root: root})
	if got := snapshot(t, root); len(got) != 0 {
		t.Fatalf("root changed: %v", got)
	}
	for _, rel := range []string{".claude/settings.json", ".claude/skills/duolog/SKILL.md", ".codex/hooks.json", ".agents/skills/duolog/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(home, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
}

func TestNeverTouchesProcessHome(t *testing.T) {
	home, root := setup(t)
	trap := os.Getenv("HOME")
	run(t, Options{Scope: "user", Agent: "both", Home: home, Root: root})
	run(t, Options{Scope: "project", Agent: "both", Home: home, Root: root})
	if err := Remove(Options{Scope: "user", Agent: "both", Home: home, Root: root}); err != nil {
		t.Fatal(err)
	}
	if got := snapshot(t, trap); len(got) != 0 {
		t.Fatalf("process home changed: %v", got)
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	home, root := setup(t)
	o := Options{Scope: "project", Agent: "both", Home: home, Root: root}
	run(t, o)
	first := snapshot(t, root)
	run(t, o)
	if second := snapshot(t, root); !equal(first, second) {
		t.Fatal("second run changed files (or left a backup)")
	}
	var doc struct {
		Hooks map[string][]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(read(t, filepath.Join(root, ".claude/settings.json"))), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Hooks["PreToolUse"]) != 1 || len(doc.Hooks["SessionStart"]) != 2 {
		t.Fatalf("hook counts: %v", doc.Hooks)
	}
}

func TestPreservesForeignHooks(t *testing.T) {
	home, root := setup(t)
	path := filepath.Join(root, ".claude/settings.json")
	foreign := `{"model":"opus","hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"other-a"}]},{"hooks":[{"type":"command","command":"other-b"}]}],"Notification":[{"hooks":[{"type":"command","command":"other-c"}]}]}}`
	write(t, path, foreign)
	o := Options{Scope: "project", Agent: "claude", Home: home, Root: root}
	run(t, o)
	run(t, o)
	got := read(t, path)
	a, b := strings.Index(got, "other-a"), strings.Index(got, "other-b")
	if a < 0 || b < a || !strings.Contains(got, "other-c") || !strings.Contains(got, `"model": "opus"`) {
		t.Fatalf("foreign content lost or reordered:\n%s", got)
	}
	if !strings.Contains(got, "duolog gate") {
		t.Fatalf("our hook missing:\n%s", got)
	}
	if read(t, path+".duolog.bak") != foreign {
		t.Fatal("backup is not the original file")
	}
}

func TestPreservesTomlComments(t *testing.T) {
	home, root := setup(t)
	toml := "# my codex settings\nmodel = \"x\" # keep me\n\n[projects.\"/tmp/p\"]\ntrust_level = \"trusted\"\n"
	path := filepath.Join(home, ".codex/config.toml")
	write(t, path, toml)
	run(t, Options{Scope: "user", Agent: "codex", Home: home, Root: root})
	if got := read(t, path); got != toml {
		t.Fatalf("config.toml changed:\n%s", got)
	}
	if err := Remove(Options{Scope: "user", Agent: "codex", Home: home, Root: root}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != toml {
		t.Fatalf("config.toml changed by uninstall:\n%s", got)
	}
}

func TestUninstallRemovesOnlyOurs(t *testing.T) {
	home, root := setup(t)
	path := filepath.Join(root, ".claude/settings.json")
	write(t, path, `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"other-a"}]}]}}`)
	o := Options{Scope: "project", Agent: "both", Home: home, Root: root}
	run(t, o)
	if err := Remove(o); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if strings.Contains(got, "duolog") || !strings.Contains(got, "other-a") {
		t.Fatalf("settings after uninstall:\n%s", got)
	}
	for _, rel := range []string{".claude/skills/duolog", ".agents/skills/duolog"} {
		if _, err := os.Stat(filepath.Join(root, rel)); !os.IsNotExist(err) {
			t.Errorf("%s still there", rel)
		}
	}
	if strings.Contains(read(t, filepath.Join(root, ".codex/hooks.json")), "duolog") {
		t.Fatal("codex hooks still ours")
	}
	if _, err := os.Stat(filepath.Join(root, ".duolog/config.yaml")); err != nil {
		t.Fatal("uninstall removed the user's config")
	}
}

func TestBadExistingJSONRefused(t *testing.T) {
	home, root := setup(t)
	bad := filepath.Join(root, ".codex/hooks.json")
	write(t, bad, `{"hooks": [`)
	write(t, filepath.Join(root, ".claude/settings.json"), `{}`)
	before := snapshot(t, root)
	_, err := Run(Options{Scope: "project", Agent: "both", Home: home, Root: root})
	if err == nil || !strings.Contains(err.Error(), bad) {
		t.Fatalf("error should name %s: %v", bad, err)
	}
	if after := snapshot(t, root); !equal(before, after) {
		t.Fatal("a refused install changed files")
	}
}

func TestHookTimeoutAboveAskTimeout(t *testing.T) {
	home, root := setup(t)
	write(t, filepath.Join(root, ".duolog/config.yaml"), "version: 1\npolicy:\n  ask_timeout_seconds: 300\n")
	run(t, Options{Scope: "project", Agent: "both", Home: home, Root: root})
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	want := cfg.Policy.AskTimeoutSeconds + 30
	for _, rel := range []string{".claude/settings.json", ".codex/hooks.json"} {
		var doc struct {
			Hooks map[string][]struct {
				Hooks []struct {
					Timeout int `json:"timeout"`
				} `json:"hooks"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal([]byte(read(t, filepath.Join(root, rel))), &doc); err != nil {
			t.Fatal(err)
		}
		for _, event := range []string{"PreToolUse", "PermissionRequest"} {
			groups := doc.Hooks[event]
			if len(groups) != 1 || groups[0].Hooks[0].Timeout < want {
				t.Errorf("%s %s: timeout %v, want at least %d", rel, event, groups, want)
			}
		}
	}
}

func TestPrintsCodexTrustStep(t *testing.T) {
	home, root := setup(t)
	r := run(t, Options{Scope: "project", Agent: "codex", Home: home, Root: root})
	text := strings.Join(r.Notes, "\n")
	if !strings.Contains(text, "/hooks") || !strings.Contains(text, "trust") {
		t.Fatalf("notes: %q", text)
	}
	r = run(t, Options{Scope: "project", Agent: "claude", Home: home, Root: root})
	if strings.Contains(strings.Join(r.Notes, "\n"), "/hooks") {
		t.Fatal("a Claude-only install mentions Codex trust")
	}
}

func TestCreatesConfigWithModeline(t *testing.T) {
	home, root := setup(t)
	run(t, Options{Scope: "project", Agent: "claude", Home: home, Root: root})
	got := read(t, filepath.Join(root, ".duolog/config.yaml"))
	first, _, _ := strings.Cut(got, "\n")
	if first != "# yaml-language-server: $schema="+config.SchemaURL {
		t.Fatalf("first line %q", first)
	}
	if _, err := config.Load(root); err != nil {
		t.Fatal(err)
	}
}

func TestKeepsExistingConfig(t *testing.T) {
	home, root := setup(t)
	path := filepath.Join(root, ".duolog/config.yaml")
	write(t, path, "version: 1\n")
	run(t, Options{Scope: "project", Agent: "both", Home: home, Root: root})
	if got := read(t, path); got != "version: 1\n" {
		t.Fatalf("config changed: %q", got)
	}
	if _, err := os.Stat(path + ".duolog.bak"); !os.IsNotExist(err) {
		t.Fatal("backup of an untouched file")
	}
}

func words(s string) int { return len(strings.Fields(s)) }

func TestSkillIsShort(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		text := skillText(t, agent)
		if n := words(text); n >= 400 {
			t.Errorf("%s skill has %d words", agent, n)
		}
		for _, rule := range []string{"duolog delegate", "task.md", "Goal", "Read", "Write", "Limits", "Check", "range", "run here: self", "run here: subagent", "duolog allow", "duolog deny", "duolog peer", "/model", "/effort"} {
			if !strings.Contains(text, rule) {
				t.Errorf("%s skill lacks %q", agent, rule)
			}
		}
	}
}

func TestSkillExplainsRunHere(t *testing.T) {
	claude, codex := skillText(t, "claude"), skillText(t, "codex")
	for _, want := range []string{"Agent tool", "model", "effort"} {
		if !strings.Contains(claude, want) {
			t.Errorf("claude skill lacks %q", want)
		}
	}
	for _, want := range []string{"spawn_agent", "model", "reasoning_effort", `fork_turns "none"`} {
		if !strings.Contains(codex, want) {
			t.Errorf("codex skill lacks %q", want)
		}
	}
	if strings.Contains(claude, "spawn_agent") || strings.Contains(codex, "Agent tool") {
		t.Error("a skill names the other agent's subagent tool")
	}
}

func skillText(t *testing.T, agent string) string {
	t.Helper()
	data, err := plugin.FS.ReadFile(agent + "/skills/duolog/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPluginHooksFileMatchesGenerated(t *testing.T) {
	data, err := plugin.FS.ReadFile("claude/hooks/hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	want, err := HooksJSON("claude", "linux", 150)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(want) {
		t.Fatalf("plugin/claude/hooks/hooks.json is stale; regenerate it from install.HooksJSON(\"claude\", \"linux\", 150)\n%s", want)
	}
}

func TestManifests(t *testing.T) {
	root := filepath.Join("..", "..")
	for path, keys := range map[string][]string{
		"plugin/claude/.claude-plugin/plugin.json": {"name", "version", "description"},
		"plugin/codex/plugin.json":                 {"$schema", "name", "version", "description"},
		".claude-plugin/marketplace.json":          {"name", "plugins"},
		".agents/plugins/marketplace.json":         {"name", "plugins"},
	} {
		var doc map[string]any
		if err := json.Unmarshal([]byte(read(t, filepath.Join(root, path))), &doc); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, k := range keys {
			if _, ok := doc[k]; !ok {
				t.Errorf("%s lacks %q", path, k)
			}
		}
	}
}

func TestGuards(t *testing.T) {
	posix := regexp.MustCompile(`command -v duolog >/dev/null 2>&1`)
	win := "where duolog >nul 2>nul"
	for _, goos := range []string{"linux", "windows"} {
		data, err := HooksJSON("claude", goos, 150)
		if err != nil {
			t.Fatal(err)
		}
		s := string(data)
		if goos == "linux" && !posix.MatchString(s) || goos == "windows" && !strings.Contains(s, win) {
			t.Errorf("%s: guard missing:\n%s", goos, s)
		}
		if !strings.Contains(s, "exit") {
			t.Errorf("%s: worker fail-closed missing", goos)
		}
	}
}
