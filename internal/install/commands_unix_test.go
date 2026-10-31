//go:build !windows

package install

import (
	"github.com/lorem-dev/duolog/internal/testbin"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Claude shell field explicitly selects Bash. Also exercise these exact
// portable guards under sh in TestGeneratedCommandsPassThrough.
func TestGeneratedCommandsWithBash(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		for _, event := range []string{"SessionStart", "SessionEnd", "UserPromptSubmit", "PostToolUse", "PreToolUse", "PermissionRequest"} {
			for _, command := range hookCommands(t, agent, event) {
				cmd := testbin.GitBashProcess(command)
				cmd.Env = append(os.Environ(), "PATH="+t.TempDir(), "DUOLOG_DEPTH=0")
				if out, err := cmd.CombinedOutput(); err != nil || len(out) != 0 {
					t.Fatalf("missing lead: %v %q", err, out)
				}
				cmd = testbin.GitBashProcess(command)
				cmd.Env = append(os.Environ(), "PATH="+t.TempDir(), "DUOLOG_DEPTH=1")
				out, err := cmd.CombinedOutput()
				want := 0
				if event == "PreToolUse" || event == "PermissionRequest" {
					want = 2
				}
				got := 0
				if err != nil {
					if cmd.ProcessState == nil {
						t.Fatal(err)
					}
					got = cmd.ProcessState.ExitCode()
				}
				if got != want || len(out) != 0 {
					t.Fatalf("missing worker: %d %v %q", got, err, out)
				}
				fake := testbin.Install(t, "duolog", testbin.Scenario{Exit: 7})
				cmd = testbin.GitBashProcess(command)
				cmd.Stdin = strings.NewReader("input\n")
				if out, err := cmd.CombinedOutput(); err == nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 7 || len(out) != 0 {
					t.Fatalf("present: %v %q", err, out)
				}
				input, err := os.ReadFile(filepath.Clean(fake) + ".stdin")
				if err != nil || string(input) != "input\n" {
					t.Fatal(string(input), err)
				}
			}
		}
	}
}
