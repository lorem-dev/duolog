package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/lorem-dev/duolog/internal/adapter/mcp"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/core/taskspec"
)

// writeTaskFile renders t into <workRoot>/.duolog/tasks/<name>/task.md and
// returns the path relative to workRoot, slash-separated. The name is one
// folder name; a task that taskspec.Render refuses writes nothing.
func writeTaskFile(workRoot, name string, t mcp.Task) (string, error) {
	data, err := taskspec.Render(taskspec.Spec(t))
	if err != nil {
		return "", err
	}
	return writeTaskText(workRoot, name, data)
}

// writeTaskText writes data as <workRoot>/.duolog/tasks/<name>/task.md and
// returns the path relative to workRoot, slash-separated.
func writeTaskText(workRoot, name string, data []byte) (string, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("task folder name %q must be one plain name", name)
	}
	rel := path.Join(".duolog", "tasks", name, "task.md")
	full := filepath.Join(workRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(full, data, 0o600); err != nil {
		return "", err
	}
	return rel, nil
}

// stdinTask is the task file name that reads the task from standard input.
const stdinTask = "-"

// maxStdinTask bounds the task text read from standard input.
const maxStdinTask = 1 << 20

// stdinTaskFile reads the task text from in and keeps it as
// .duolog/tasks/<id>/task.md under workRoot, where duolog keeps its task
// copies, so the lead leaves no task file of its own behind. It returns the
// file's full path. A terminal, an empty input and an input over
// maxStdinTask bytes write nothing.
func stdinTaskFile(in io.Reader, workRoot string) (string, error) {
	if f, ok := in.(*os.File); ok {
		if info, err := f.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
			return "", errors.New("the task file - reads the task from standard input, which is a terminal; pipe the task text in")
		}
	}
	data, err := io.ReadAll(io.LimitReader(in, maxStdinTask+1))
	if err != nil {
		return "", fmt.Errorf("cannot read the task from standard input: %w", err)
	}
	if len(data) > maxStdinTask {
		return "", fmt.Errorf("the task on standard input is over %d bytes; put long material in files and name them under ## Read", maxStdinTask)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return "", errors.New("standard input holds no task; pipe the task text in")
	}
	rel, err := writeTaskText(workRoot, delegate.NewRecordID(), data)
	if err != nil {
		return "", err
	}
	return filepath.Join(workRoot, filepath.FromSlash(rel)), nil
}
