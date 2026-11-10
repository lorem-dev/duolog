package delegate

import (
	"os"
	"path/filepath"
)

// Each attempt keeps its own logs and a copy of the latest task, including
// any partial-work note from the previous candidate.
func newAttemptDir(base string) (string, error) {
	task, err := os.ReadFile(filepath.Join(base, "task.md"))
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(base, "attempt-")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "task.md"), task, 0600); err != nil {
		return "", err
	}
	return dir, nil
}
