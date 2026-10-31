package install

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
								cmd := testbin.HookProcess(command)
								if shell == "bash" {
									cmd = testbin.GitBashProcess(command)
								}
								ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
								t.Cleanup(cancel)
								bounded := testbin.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
								bounded.SysProcAttr = cmd.SysProcAttr
								return bounded
							}
							for _, depth := range []string{"0", "1"} {
								t.Setenv("DUOLOG_DEPTH", depth)
								cmd := process()
								cmd.Env = append(os.Environ(), "PATH="+filepath.Join(os.Getenv("SYSTEMROOT"), "System32")+";"+filepath.Join(os.Getenv("SYSTEMROOT"), "System32", "WindowsPowerShell", "v1.0"))
								started := time.Now()
								out, err := cmd.CombinedOutput()
								t.Logf("missing %s depth=%s took %s", event, depth, time.Since(started))
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
							started := time.Now()
							out, err := cmd.CombinedOutput()
							t.Logf("present %s took %s", event, time.Since(started))
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

// A worker has no PSModulePath unless it is passed through, and every Codex tool
// call runs this guard, so the guard must not need a module search.
func TestWindowsGuardNeedsNoModuleSearch(t *testing.T) {
	data, err := HooksJSON("codex", "windows", 150)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct{ Hooks map[string][]group }
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	testbin.Install(t, "duolog", testbin.Scenario{Exit: 7})
	var env []string
	for _, name := range []string{"PATH", "SYSTEMROOT", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP", "PATHEXT", "COMSPEC"} {
		env = append(env, name+"="+os.Getenv(name))
	}
	command := testbin.HookProcess(doc.Hooks["PreToolUse"][0].Hooks[0].CommandWindows)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := testbin.CommandContext(ctx, command.Path, command.Args[1:]...)
	cmd.SysProcAttr = command.SysProcAttr
	cmd.Env = append(env, "DUOLOG_DEPTH=1")
	started := time.Now()
	err = cmd.Run()
	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 7 {
		t.Fatal(err)
	}
	if took := time.Since(started); took > 10*time.Second {
		t.Fatalf("guard took %s", took)
	}
}
