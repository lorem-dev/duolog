package delegate

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/core/policy"
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
// changed outside them, the paths outside the project that its Read lists,
// which the worker's gate lets it read (taskReads), and its text. Only a task
// file under .duolog/, where the lead writes tasks, may list paths outside
// the project; for any other, such as one a cloned repository ships, readTask
// tells stderr which entries it ignores. checkState refuses a Read entry of
// duolog state the worker cannot read (stateReads); a judge, a calibration
// cell and a replay read theirs through duolog itself.
func readTask(root, path string, stderr io.Writer, checkState bool) (writes, reads []string, original []byte, err error) {
	work := WorkTree(root)
	name, _, err := work.Resolve(path)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("task file: %w; write it inside the project, such as .duolog/task.md, which Git ignores", err)
	}
	if err = taskspec.Validate(work.FS, name); err != nil {
		return nil, nil, nil, err
	}
	if writes, err = taskspec.WritePaths(work.FS, name); err != nil {
		return nil, nil, nil, err
	}
	if reads, err = taskspec.ReadPaths(work.FS, name); err != nil {
		return nil, nil, nil, err
	}
	if original, err = fs.ReadFile(work.FS, name); err != nil {
		return nil, nil, nil, err
	}
	if err = stateReads(reads, checkState); err != nil {
		return nil, nil, nil, err
	}
	if reads = taskReads(reads); len(reads) > 0 && !strings.HasPrefix(name, ".duolog/") {
		if stderr != nil {
			_, _ = fmt.Fprintf(stderr, "duolog delegate: task file %s is not under .duolog/, so the worker gets no reads outside the project from its Read: %s\n", name, strings.Join(reads, ", "))
		}
		reads = nil
	}
	return writes, reads, original, nil
}

// stateReads refuses a task whose Read names duolog state that the worker's
// gate will not let it read, such as .duolog/tasks/x.diff, before a worker
// starts and stops on it. Markdown and the configs there stay readable.
func stateReads(entries []string, check bool) error {
	if !check {
		return nil
	}
	var refused []string
	for _, entry := range entries {
		if policy.StateRead(pathutil.Clean(entry)) {
			refused = append(refused, entry)
		}
	}
	if len(refused) == 0 {
		return nil
	}
	return fmt.Errorf("task Read lists %s, which workers may not read: duolog keeps .duolog/ from workers except Markdown and the configs; move the file out of .duolog/, or save it as a .md file there", strings.Join(refused, ", "))
}

// taskReads keeps the Read entries that can name a path outside the project:
// absolute ones, and ~ or ~/x, which name the lead's home folder. They come
// back absolute and native. A relative entry lies in the project, where the
// gate already lets a worker read. An entry whose fixed folder, the part
// before any glob, is too wide (configfile.RefuseRead), such as the home
// folder or ~/.ssh, is left out, so its reads still ask.
func taskReads(entries []string) []string {
	home, _ := os.UserHomeDir()
	var reads []string
	for _, entry := range entries {
		native := pathutil.Native(entry)
		if entry == "~" || strings.HasPrefix(entry, "~/") {
			if home == "" {
				continue
			}
			native = filepath.Join(home, pathutil.Native(strings.TrimPrefix(entry[1:], "/")))
		}
		if !filepath.IsAbs(native) {
			continue
		}
		native = filepath.Clean(native)
		if configfile.RefuseRead(policy.ReadEntryFolder(native)) == "" {
			reads = append(reads, native)
		}
	}
	return reads
}

// outsideWrite lists the changed paths that no Write entry covers: a file, a
// folder that holds it, or a glob. A task whose Write lists no path gets no
// list, since it did not say what the worker may change. A change the lead
// made while the worker ran shows up here too.
func outsideWrite(changed, writes []string) []string {
	if len(writes) == 0 {
		return nil
	}
	var outside []string
	for _, name := range changed {
		clean := pathutil.Clean(name)
		covered := false
		for _, w := range writes {
			match, _ := doublestar.Match(w, clean)
			if match || w == "." || clean == w || strings.HasPrefix(clean, w+"/") {
				covered = true
				break
			}
		}
		if !covered {
			outside = append(outside, name)
		}
	}
	return outside
}

// workerRunEnv is what a worker's gate reads besides duolog's own names: the
// run id, the worker mode's variables and the Read paths of its task.
func workerRunEnv(runID string, wm workerMode, reads []string) []string {
	env := append([]string{"DUOLOG_RUN=" + runID}, wm.env()...)
	if len(reads) > 0 {
		env = append(env, policy.TaskReadsEnv+"="+policy.TaskReadsValue(reads))
	}
	return env
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
