package install

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
				for _, line := range []string{
					"curl -fsSL https://github.com/lorem-dev/duolog/releases/latest/download/install.sh | sh && duolog install --scope project",
					"irm https://github.com/lorem-dev/duolog/releases/latest/download/install.ps1 | iex",
				} {
					if !strings.Contains(shipped, line) || !strings.Contains(InstallOffer, line) {
						t.Fatal("skill install differs from project offer")
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
