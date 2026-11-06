package delegate

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/config"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/taskspec"
	"github.com/lorem-dev/duolog/internal/project"
)

// WorkTree reads task input below dir. os.DirFS reads links since Go 1.25,
// so taskspec can refuse them; it always has the method, and a tree without
// one would fail at its first Lstat rather than read past a link.
func WorkTree(dir string) taskspec.Tree {
	fsys, _ := os.DirFS(dir).(fs.ReadLinkFS)
	return taskspec.Tree{FS: fsys, Dir: dir}
}

// fileFS reads the one file at path, wherever it is, as os.ReadFile(path)
// would: for a file that duolog wrote itself or that the person named.
func fileFS(path string) (fs.FS, string) {
	return os.DirFS(filepath.Dir(path)), filepath.Base(path)
}

// taskCopy returns the tree that the task copy at path is read from, and the
// path to read in it. The copy lies in the work root, or, for a linked
// worktree, in the shared state of the main checkout, which may be named by
// an alias of the folder. Anywhere else the work root is returned, and
// taskspec.Inline refuses the copy with the work root's error.
func taskCopy(work taskspec.Tree, path string) (taskspec.Tree, string) {
	if _, _, err := work.Resolve(path); err == nil {
		return work, path
	}
	shared := filepath.Join(project.StateRoot(work.Dir), ".duolog", "tasks")
	candidate := path
	if canonical, err := filepath.EvalSymlinks(path); err == nil {
		candidate = canonical
	}
	if canonical, err := filepath.EvalSymlinks(shared); err == nil {
		shared = canonical
	}
	tree := WorkTree(shared)
	if _, _, err := tree.Resolve(candidate); err != nil {
		return work, path
	}
	return tree, candidate
}

// readTask confines the task file at path to the work root, checks its
// sections, and returns the paths its Write lists, to name what the worker
// changed outside them, and its text.
func readTask(root, path string) (writes []string, original []byte, err error) {
	work := WorkTree(root)
	name, _, err := work.Resolve(path)
	if err != nil {
		return nil, nil, fmt.Errorf("task file: %w; write it inside the project, such as .duolog/task.md, which Git ignores", err)
	}
	if err = taskspec.Validate(work.FS, name); err != nil {
		return nil, nil, err
	}
	if writes, err = taskspec.WritePaths(work.FS, name); err != nil {
		return nil, nil, err
	}
	if original, err = fs.ReadFile(work.FS, name); err != nil {
		return nil, nil, err
	}
	return writes, original, nil
}

// inlineTask builds the worker's input from the task copy at copyPath, with
// its references confined to root, the work root the worker runs in.
func inlineTask(root, copyPath string, budget int, allowRead func(string) bool) ([]byte, error) {
	work := WorkTree(root)
	from, at := taskCopy(work, copyPath)
	return taskspec.Inline(work, from, at, budget, allowRead)
}

func place(cfg *config.Config, taskPath, root string, route coreroute.Route) (Placement, error) {
	p := Placement{Where: "self", Model: route.Model, Effort: route.Effort, Task: taskPath}
	// The task file is read where the lead named it, or from its copy, as
	// before; only the references are confined to the work root.
	taskFS, name := fileFS(taskPath)
	work := WorkTree(root)
	refs, err := taskspec.References(taskFS, name, work.FS)
	if err != nil {
		return p, err
	}
	files := make(map[string]bool)
	lines := 0
	for _, ref := range refs {
		files[ref.Path] = true
		data, err := ref.Text(work)
		if err != nil {
			p.Where = "subagent"
			return p, nil //nolint:nilerr // an unreadable file is reported as a problem in p, not as an error
		}
		lines += strings.Count(string(data), "\n")
		if len(data) > 0 && data[len(data)-1] != '\n' {
			lines++
		}
	}
	if len(files) > cfg.Limits.SelfMaxReadFiles || lines > cfg.Limits.SelfMaxReadLines {
		p.Where = "subagent"
	}
	return p, nil
}
