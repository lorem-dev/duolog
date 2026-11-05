package install

import (
	"bytes"
	"encoding/json"
	"github.com/lorem-dev/duolog/plugin"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/testbin"
)

// Run every generated hook against a real executable, rather than asserting on
// substrings. The compiled recording executable observes raw stdin and argv.
func TestGeneratedCommandsPassThrough(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		for _, event := range []string{"SessionStart", "SessionEnd", "UserPromptSubmit", "PostToolUse", "PreToolUse", "PermissionRequest"} {
			for _, code := range []int{0, 7} {
				t.Run(agent+"/"+event+"/"+string(rune('0'+code)), func(t *testing.T) {
					fake := testbin.Install(t, "duolog", testbin.Scenario{Exit: code, Stdout: "stdout", Stderr: "stderr"})
					input := "{\"cwd\":\"C:\\\\project\",\"text\":\"caf\u00e9 \u4e16\u754c\"}\n"
					for _, command := range hookCommands(t, agent, event) {
						cmd := testbin.HookProcess(command.Command, command.Args...)
						cmd.Stdin = strings.NewReader(input)
						var stdout, stderr bytes.Buffer
						cmd.Stdout, cmd.Stderr = &stdout, &stderr
						err := cmd.Run()
						got := 0
						if err != nil {
							if cmd.ProcessState == nil {
								t.Fatal(err)
							}
							got = cmd.ProcessState.ExitCode()
						}
						if got != code || stdout.String() != "stdout" || stderr.String() != "stderr" {
							t.Fatalf("exit %d: %v stdout=%q stderr=%q", got, err, &stdout, &stderr)
						}
						data, err := os.ReadFile(fake + ".stdin")
						if err != nil || string(data) != input {
							t.Fatalf("stdin=%q %v", data, err)
						}
					}
				})
			}
		}
	}
}

func TestUserConfigOverrides(t *testing.T) {
	home, root := setup(t)
	claude, codex := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	t.Setenv("CODEX_HOME", codex)
	run(t, Options{Scope: "user", Agent: "both", Home: home, Root: root})
	for _, path := range []string{filepath.Join(claude, "settings.json"), filepath.Join(claude, "skills", "duolog", "SKILL.md"), filepath.Join(codex, "hooks.json")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}

}

func TestProjectCanDisableUserPlugin(t *testing.T) {
	home, root := setup(t)
	write(t, filepath.Join(home, ".claude", "settings.json"), `{"enabledPlugins":{"duolog@duolog":true}}`)
	write(t, filepath.Join(root, ".claude", "settings.json"), `{"enabledPlugins":{"duolog@duolog":false}}`)
	run(t, Options{Scope: "project", Agent: "claude", Home: home, Root: root})
	if !strings.Contains(read(t, filepath.Join(root, ".claude", "settings.json")), `"command": "duolog"`) {
		t.Fatal("disabled plugin prevented native hook install")
	}
}

func TestExecutableHookOwnership(t *testing.T) {
	for _, raw := range []string{
		`{"hooks":[{"type":"command","command":"duolog","args":["gate","--agent","claude"]}]}`,
		`{"hooks":[{"type":"command","commandWindows":"powershell -NoProfile -NonInteractive -Command duolog gate --agent codex"}]}`,
	} {
		if _, ours, err := withoutOurHandlers([]byte(raw)); err != nil || !ours {
			t.Fatal("hook not recognized", raw, err)
		}
	}
}

func TestUserPluginOverrideIgnoresDefaultHome(t *testing.T) {
	home, root := setup(t)
	custom := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", custom)
	write(t, filepath.Join(home, ".claude", "settings.json"), `{"enabledPlugins":{"duolog@duolog":true}}`)
	run(t, Options{Scope: "user", Agent: "claude", Home: home, Root: root})
	if !strings.Contains(read(t, filepath.Join(custom, "settings.json")), `"command": "duolog"`) {
		t.Fatal("read the default home despite CLAUDE_CONFIG_DIR")
	}
}

func TestSkillsCheckBinaryAndRules(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		for _, scope := range []string{"project", "user"} {
			t.Run(agent+"/"+scope, func(t *testing.T) {
				home, root := setup(t)
				o := Options{Scope: scope, Agent: agent, Home: home, Root: root}
				run(t, o)
				base := root
				if scope == "user" {
					base = home
				}
				shipped := skillText(t, agent)
				copied := read(t, installedSkillPath(o, base, agent))
				if copied != shipped {
					t.Fatal("installed skill differs from plugin")
				}
				if len(strings.Fields(shipped)) >= 400 {
					t.Fatal("skill must stay under 400 words")
				}
				for _, want := range []string{"Run `duolog version`", "not found", "tell the user", "only if they agree", "Run `duolog doctor` once per session", "rules are off", "what it names", "`/hooks`"} {
					if !strings.Contains(shipped, want) {
						t.Fatalf("skill missing %q", want)
					}
				}
				// A project's offer stays on latest, because it outlives any one version. The
				// skill names its own version, which version.py keeps current.
				for _, line := range []string{
					"curl -fsSL https://github.com/lorem-dev/duolog/releases/latest/download/install.sh | sh && duolog install --scope project",
					"irm https://github.com/lorem-dev/duolog/releases/latest/download/install.ps1 | iex",
				} {
					if !strings.Contains(InstallOffer, line) {
						t.Fatal("project offer changed: " + line)
					}
				}
				for _, pattern := range []string{
					`curl -fsSL https://github\.com/lorem-dev/duolog/releases/download/v[0-9.]+/install\.sh \| sh -s -- --version v[0-9.]+ && duolog install --scope project`,
					`\$env:DUOLOG_VERSION = 'v[0-9.]+'; irm https://github\.com/lorem-dev/duolog/releases/download/v[0-9.]+/install\.ps1 \| iex`,
				} {
					if !regexp.MustCompile(pattern).MatchString(shipped) {
						t.Fatal("skill does not pin its install: " + pattern)
					}
				}
				if strings.Index(shipped, "duolog version") > strings.Index(shipped, "duolog doctor") || strings.Index(shipped, "duolog doctor") > strings.Index(shipped, "## Hand over") {
					t.Fatal("checks must come first")
				}
			})
		}
	}
}

func TestWindowsGuardAvoidsCommandDiscovery(t *testing.T) {
	data, err := HooksJSON("codex", 150)
	if err != nil {
		t.Fatal(err)
	}
	for _, slow := range []string{"Get-Command", "Get-Variable", "Test-Path"} {
		if strings.Contains(string(data), slow) {
			t.Errorf("%s searches modules for 24 seconds on every Codex tool call", slow)
		}
	}
	if !strings.Contains(string(data), "where.exe") {
		t.Error("guard no longer finds duolog with where.exe")
	}
}

func TestReinstallUpgradesOlderClaudeHooks(t *testing.T) {
	for _, command := range []string{
		"if command -v duolog >/dev/null 2>&1; then exec duolog gate --agent claude --event PreToolUse; fi",
		"if (Get-Command duolog -ErrorAction SilentlyContinue) { duolog gate --agent claude; exit $LASTEXITCODE }; exit 0",
		"duolog",
	} {
		home, root := setup(t)
		path := filepath.Join(root, ".claude", "settings.json")
		raw, err := json.Marshal(map[string]any{"hooks": map[string]any{"PreToolUse": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "shell": "bash", "args": []string{"gate"}}}}}}})
		if err != nil {
			t.Fatal(err)
		}
		write(t, path, string(raw))
		run(t, Options{Scope: "project", Agent: "claude", Home: home, Root: root})
		want, _ := HooksJSON("claude", 150)
		if read(t, path) != string(want) {
			t.Fatal("legacy hook was not replaced", read(t, path))
		}
	}
}

// TestTelemetryInstallKeepsOneHookPerEvent: installing twice leaves one duolog
// hook per event and command, and the person's sandbox exactly as it was. The
// store roots go to workers' launch settings, never to installed settings.
func TestTelemetryInstallKeepsOneHookPerEvent(t *testing.T) {
	for _, scope := range []string{"user", "project"} {
		t.Run(scope, func(t *testing.T) {
			home, root := setup(t)
			path := filepath.Join(root, ".claude", "settings.json")
			if scope == "user" {
				path = filepath.Join(home, ".claude", "settings.json")
			}
			const sandbox = `{"enabled":false,"filesystem":{"denyRead":["~/.ssh"]}}`
			write(t, path, `{"sandbox":`+sandbox+`}`)
			o := Options{Scope: scope, Agent: "claude", Home: home, Root: root}
			run(t, o)
			run(t, o)
			var doc struct {
				Sandbox json.RawMessage `json:"sandbox"`
				Hooks   map[string][]struct {
					Hooks []struct {
						Command string   `json:"command"`
						Args    []string `json:"args"`
					} `json:"hooks"`
				} `json:"hooks"`
			}
			got := read(t, path)
			if err := json.Unmarshal([]byte(got), &doc); err != nil {
				t.Fatal(err)
			}
			var compact bytes.Buffer
			if err := json.Compact(&compact, doc.Sandbox); err != nil || compact.String() != sandbox {
				t.Fatalf("sandbox changed: %s %v", doc.Sandbox, err)
			}
			if strings.Contains(got, "telemetry") || strings.Contains(got, "balance") {
				t.Fatalf("installed settings name a store:\n%s", got)
			}
			for _, event := range []string{"SessionStart", "SessionEnd", "UserPromptSubmit", "PostToolUse", "PreToolUse", "PermissionRequest"} {
				seen := map[string]int{}
				for _, group := range doc.Hooks[event] {
					for _, h := range group.Hooks {
						if IsOurCommand(h.Command) {
							seen[strings.Join(h.Args, " ")]++
						}
					}
				}
				if len(seen) == 0 {
					t.Fatalf("%s has no duolog hook:\n%s", event, got)
				}
				for args, n := range seen {
					if n != 1 {
						t.Fatalf("%s: %q installed %d times", event, args, n)
					}
				}
			}
		})
	}
}

// TestTelemetryLeadHookSeesEveryTool: the lead's PreToolUse hook has no
// matcher, so every Read, Grep and shell call reaches the lead guard that
// keeps telemetry and balance for the person, and the skill pre-allows no
// telemetry or balance command.
func TestTelemetryLeadHookSeesEveryTool(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		data, err := HooksJSON(agent, 150)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Hooks map[string][]struct {
				Matcher *string `json:"matcher"`
				Hooks   []struct {
					Command string   `json:"command"`
					Args    []string `json:"args"`
				} `json:"hooks"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		gates := 0
		for _, group := range doc.Hooks["PreToolUse"] {
			for _, h := range group.Hooks {
				if !strings.Contains(h.Command+" "+strings.Join(h.Args, " "), "gate") {
					continue
				}
				gates++
				if group.Matcher != nil && *group.Matcher != "" && *group.Matcher != "*" {
					t.Fatalf("%s lead gate only sees %q", agent, *group.Matcher)
				}
			}
		}
		if gates == 0 {
			t.Fatalf("%s has no PreToolUse gate:\n%s", agent, data)
		}
	}
	for _, path := range []string{"claude/skills/duolog/SKILL.md", "codex/skills/duolog/SKILL.md"} {
		data, err := plugin.FS.ReadFile(path)
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(l, "allowed-tools:") && (strings.Contains(l, "telemetry") || strings.Contains(l, "balance")) {
				t.Fatalf("%s pre-allows a person-only command: %s", path, l)
			}
		}
	}
}

// A read-only list: nothing here starts a worker, decides for the lead or writes.
func TestClaudeSkillAllowsOnlyReportCommands(t *testing.T) {
	data, err := plugin.FS.ReadFile("claude/skills/duolog/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	line := ""
	for _, l := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(l, "allowed-tools:") {
			line = l
		}
	}
	if !strings.Contains(line, "Bash(duolog version)") || !strings.Contains(line, "Bash(duolog doctor)") {
		t.Fatal("report commands are missing: " + line)
	}
	for _, refused := range []string{"duolog *", "delegate", "allow", "deny", "mode", "install", "uninstall", "sync", "send", "peer open", "peer answer", "peer close", "curl", "irm"} {
		if strings.Contains(strings.TrimPrefix(line, "allowed-tools:"), refused) {
			t.Fatalf("the skill must not allow %q: %s", refused, line)
		}
	}
}
