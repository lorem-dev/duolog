package delegate

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/core/taskspec"
)

// smallTaskLine is the answer for a task that routing.small_task_files keeps in
// the lead, or "" when the rule does not apply. A hand-over has a fixed cost,
// so a task that writes few named files costs less done in place. The rule
// leaves alone read-only roles and reviews, a target the person pinned, a
// replay, a calibration cell, an escalation, learning mode and a plan-mode
// lead, and a task whose Write section names no file, or a glob or a folder.
func smallTaskLine(cfg *config.Config, role, root, taskPath string, writes, folders []string, opts Options, leadMode mode.Mode) string {
	limit := cfg.Routing.SmallTaskFiles
	if limit <= 0 || smallTaskExempt(cfg, role, opts, leadMode) {
		return ""
	}
	n, ok := smallTaskFileCount(root, writes, folders)
	if !ok || n == 0 || n > limit {
		return ""
	}
	reason := fmt.Sprintf("the task writes %d of at most %d files (routing.small_task_files), so a worker costs more than it saves; pass --force (force: true on the MCP delegate tool) to start the worker anyway", n, limit)
	return leadLine(role, []string{reason}, Placement{Where: "self", Task: taskPath})
}

// smallTaskExempt reports whether the small-task rule leaves the run alone.
func smallTaskExempt(cfg *config.Config, role string, opts Options, leadMode mode.Mode) bool {
	if opts.Force || opts.Entry != nil || opts.Calibration || opts.Judge || opts.ReplayOf != "" || opts.EscalateFrom != "" || cfg.Learning.Enabled || leadMode == mode.Plan {
		return true
	}
	return !slices.Contains(config.SmallTaskRoles, role)
}

// smallTaskFileCount counts the distinct files that writes names. It reports
// false when an entry is a glob or a folder.
func smallTaskFileCount(root string, writes, folders []string) (int, bool) {
	files := map[string]bool{}
	for _, w := range writes {
		if strings.ContainsAny(w, "*?[{") || slices.Contains(folders, w) || isFolder(root, w) {
			return 0, false
		}
		files[w] = true
	}
	return len(files), true
}

// Snapshot is the exported snapshot, for live peer sessions; Changes is next to it in run.go.
func Snapshot(root string) (map[string]string, error) { return snapshot(root) }

// copyTask writes the task text original as task.md into a new folder under
// tasksDir, and returns the folder and the path of the copy.
func copyTask(tasksDir string, original []byte) (dir, copyPath string, err error) {
	if err = os.MkdirAll(tasksDir, 0755); err != nil {
		return "", "", err
	}
	if dir, err = os.MkdirTemp(tasksDir, "task-"); err != nil {
		return "", "", err
	}
	copyPath = filepath.Join(dir, "task.md")
	if err = os.WriteFile(copyPath, original, 0600); err != nil {
		return "", "", err
	}
	return dir, copyPath, nil
}

// taskLookup is the path to read the task at: a relative path that exists
// under the working directory is made absolute there.
func taskLookup(taskPath string) string {
	lookup := pathutil.Native(taskPath)
	if !filepath.IsAbs(lookup) {
		if cwd, e := os.Getwd(); e == nil {
			candidate := filepath.Join(cwd, lookup)
			if _, e := os.Stat(candidate); e == nil {
				lookup = candidate
			}
		}
	}
	return lookup
}

// readWriteFolders reads the Write entries of the task at path that end in a
// slash. A task that cannot be read gives none: readTask has judged it.
func readWriteFolders(root, path string) []string {
	work := WorkTree(root)
	name, _, err := work.Resolve(path)
	if err != nil {
		return nil
	}
	folders, _ := taskspec.WriteFolders(work.FS, name)
	return folders
}

// isFolder reports whether a Write entry names a folder: the work root itself,
// a path that is one on disk. An entry written with a trailing slash is a folder
// whether it exists or not; readWriteFolders finds those.
func isFolder(root, entry string) bool {
	if entry == "" || entry == "." {
		return true
	}
	native := pathutil.Native(entry)
	if !filepath.IsAbs(native) {
		native = filepath.Join(root, native)
	}
	info, err := os.Stat(native)
	return err == nil && info.IsDir()
}
