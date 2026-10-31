package install

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/testbin"
)

// Codex overrides run through CreateProcess and Git Bash.
func TestWindowsHookShells(t *testing.T) {
	for _, shell := range []string{"bash", "powershell"} {
		for _, agent := range []string{"codex"} {
			t.Run(shell+"/"+agent, func(t *testing.T) {
				var doc struct{ Hooks map[string][]group }
				data, err := HooksJSON(agent, "windows", 150)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(data, &doc); err != nil {
					t.Fatal(err)
				}

				for event, groups := range doc.Hooks {
					for _, g := range groups {
						for _, h := range g.Hooks {
							command := h.CommandWindows
							process := func() *exec.Cmd {
								if shell == "bash" {
									return testbin.GitBashProcess(command)
								}
								return testbin.HookProcess(command)
							}
							for _, depth := range []string{"0", "1"} {
								t.Setenv("DUOLOG_DEPTH", depth)
								cmd := process()
								cmd.Env = append(os.Environ(), "PATH="+filepath.Join(os.Getenv("SYSTEMROOT"), "System32")+";"+filepath.Join(os.Getenv("SYSTEMROOT"), "System32", "WindowsPowerShell", "v1.0"))
								out, err := cmd.CombinedOutput()
								got := 0
								if err != nil {
									if cmd.ProcessState == nil {
										t.Fatal(err)
									}
									got = cmd.ProcessState.ExitCode()
								}
								want := 0
								if depth == "1" && (event == "PreToolUse" || event == "PermissionRequest") {
									want = 2
								}
								if got != want || len(out) != 0 {
									t.Fatalf("missing %s depth=%s: %d %v %q", event, depth, got, err, out)
								}
							}
							fake := testbin.Install(t, "duolog", testbin.Scenario{Exit: 7, Stdout: "hello"})
							cmd := process()
							cmd.Stdin = strings.NewReader("{\"text\":\"stdin\"}\n")
							out, err := cmd.CombinedOutput()
							if err == nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 7 || string(out) != "hello" {
								t.Fatalf("present %s: %v %q", event, err, out)
							}
							stdin, err := os.ReadFile(fake + ".stdin")
							if err != nil || !bytes.Equal(stdin, []byte("{\"text\":\"stdin\"}\n")) {
								t.Fatalf("stdin %q %v", stdin, err)
							}
						}
					}
				}
			})
		}
	}
}
