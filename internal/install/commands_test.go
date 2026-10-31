package install

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
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
						cmd := testbin.HookProcess(command)
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
	if runtime.GOOS == "windows" && !strings.Contains(read(t, filepath.Join(claude, "settings.json")), `"shell": "powershell"`) {
		t.Fatal("Windows needs native PowerShell hooks")
	}
}

func TestProjectCanDisableUserPlugin(t *testing.T) {
	home, root := setup(t)
	write(t, filepath.Join(home, ".claude", "settings.json"), `{"enabledPlugins":{"duolog@duolog":true}}`)
	write(t, filepath.Join(root, ".claude", "settings.json"), `{"enabledPlugins":{"duolog@duolog":false}}`)
	run(t, Options{Scope: "project", Agent: "claude", Home: home, Root: root})
	if !strings.Contains(read(t, filepath.Join(root, ".claude", "settings.json")), "duolog gate") {
		t.Fatal("disabled plugin prevented native hook install")
	}
}

func TestExecutableHookOwnership(t *testing.T) {
	for _, raw := range []string{
		`{"hooks":[{"type":"command","command":"duolog","args":["gate","--agent","claude"]}]}`,
		`{"hooks":[{"type":"command","commandWindows":"powershell -NoProfile -NonInteractive -Command duolog gate --agent codex"}]}`,
	} {
		if !isOurs([]byte(raw)) {
			t.Fatal("hook not recognized", raw)
		}
	}
}

func TestUserPluginOverrideIgnoresDefaultHome(t *testing.T) {
	home, root := setup(t)
	custom := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", custom)
	write(t, filepath.Join(home, ".claude", "settings.json"), `{"enabledPlugins":{"duolog@duolog":true}}`)
	run(t, Options{Scope: "user", Agent: "claude", Home: home, Root: root})
	if !strings.Contains(read(t, filepath.Join(custom, "settings.json")), "duolog gate") {
		t.Fatal("read the default home despite CLAUDE_CONFIG_DIR")
	}
}
