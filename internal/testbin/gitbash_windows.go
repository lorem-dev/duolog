package testbin

import (
	"os"
	"os/exec"
	"path/filepath"
)

// GitBashProcess names the shell explicitly, as Claude's Bash hooks do. The
// absolute path also permits tests with a PATH deliberately missing all tools.
func GitBashProcess(command string) *exec.Cmd {
	path := filepath.Join(os.Getenv("ProgramFiles"), "Git", "bin", "bash.exe")
	if _, err := os.Stat(path); err != nil {
		if git, err := exec.LookPath("git"); err == nil {
			path = filepath.Join(filepath.Dir(filepath.Dir(git)), "bin", "bash.exe")
		}
	}
	return exec.Command(path, "-c", command)
}
