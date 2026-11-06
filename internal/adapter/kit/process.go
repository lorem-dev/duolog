package kit

import (
	"errors"
	"os"
	"os/exec"
	"regexp"
	"time"
)

// TaskInstruction is the one argument a worker gets besides its flags: the
// task itself comes on stdin.
const TaskInstruction = "Do the task in the stdin block."

// WriteMCPConfig writes the bridged servers for --mcp-config. CreateTemp makes
// the file 0600; the chmod keeps it so on a system that does otherwise.
func WriteMCPConfig(dir string, data []byte) (string, error) {
	f, err := os.CreateTemp(dir, "mcp-*.json")
	if err != nil {
		return "", err
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if err = errors.Join(writeErr, closeErr, os.Chmod(f.Name(), 0600)); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

var fiveHourPattern = regexp.MustCompile(`(?i)\b(?:5|five)[- ]hour`)

// FallbackReset is how long a limit without a readable reset is kept: the
// whole window for a five-hour limit, an hour otherwise, so that it ends by
// itself rather than staying until a probe.
func FallbackReset(text string, now time.Time) time.Time {
	if fiveHourPattern.MatchString(text) {
		return now.Add(5 * time.Hour)
	}
	return now.Add(time.Hour)
}

// ProcessTree makes cancelling cmd end the processes it started too, as for a
// worker.
func ProcessTree(cmd *exec.Cmd) { configureProcess(cmd) }
