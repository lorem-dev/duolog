package project

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// Workers' Claude settings deny reads of their lead home as well as their own
// .duolog. workerenv sits below this package, so it is given the resolver here.
func init() {
	workerenv.LeadHomeOf = LeadHome
}

// submoduleTimeout bounds one enumeration of duolog submodules.
const submoduleTimeout = 30 * time.Second

// Checkouts lists the checkouts of the repository at dir, its main checkout
// and its linked worktrees, as `git worktree list` names them. Prunable and
// bare entries are left out. Git names a submodule's main checkout by its Git
// folder (.git/modules/<name>); that entry is the main checkout StateRoot
// finds instead. A folder outside Git has none.
func Checkouts(ctx context.Context, dir string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if gitRootContext(ctx, dir, "--show-toplevel") == "" {
		return nil, ctx.Err()
	}
	out, err := Git(ctx, nil, "-C", dir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, fmt.Errorf("git worktree list in %s: %w %s", dir, err, strings.TrimSpace(GitStderr(err)))
	}
	checkouts := parseWorktrees(string(out))
	common := gitRootContext(ctx, dir, "--git-common-dir")
	for i, checkout := range checkouts {
		if common != "" && identity(checkout) == identity(common) {
			checkouts[i] = stateRoot(ctx, dir)
		}
	}
	return checkouts, ctx.Err()
}

// DuologSubmodules returns the checkouts of the duolog submodules of root's
// repository, at any depth: each initialized Git submodule of any checkout of
// root's repository that shares root's lead home, and the linked checkouts of
// such a module. Only Git's own records link a module: the paths in each
// checkout's .gitmodules and the module's worktree list, never the folders
// around it. Modules without duolog config and uninitialized ones are left
// out; other Git or parse failures are errors. The paths are sorted and
// unique by their resolved identity.
func DuologSubmodules(ctx context.Context, root string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, submoduleTimeout)
	defer cancel()
	w := &submoduleWalk{ctx: ctx, home: identity(leadHome(ctx, root)), seen: map[string]bool{}}
	checkouts, err := Checkouts(ctx, root)
	if err != nil {
		return nil, err
	}
	for _, checkout := range checkouts {
		w.seen[identity(checkout)] = true
	}
	for _, checkout := range checkouts {
		if err := w.modules(checkout); err != nil {
			return nil, err
		}
	}
	slices.Sort(w.out)
	return w.out, nil
}

// submoduleWalk holds one enumeration: the lead home every module must share
// and the checkouts already visited, by resolved identity.
type submoduleWalk struct {
	ctx  context.Context
	home string
	seen map[string]bool
	out  []string
}

// modules visits the duolog submodules that checkout's .gitmodules lists.
func (w *submoduleWalk) modules(checkout string) error {
	paths, err := w.modulePaths(checkout)
	if err != nil {
		return err
	}
	for _, rel := range paths {
		if err := w.ctx.Err(); err != nil {
			return err
		}
		dir := filepath.Join(checkout, filepath.FromSlash(rel))
		if !w.duologModule(checkout, dir) {
			continue
		}
		if err := w.module(dir); err != nil {
			return err
		}
	}
	return w.ctx.Err()
}

// module adds the checkouts of the module at dir, its own and its linked
// ones, and visits their modules in turn.
func (w *submoduleWalk) module(dir string) error {
	checkouts, err := Checkouts(w.ctx, dir)
	if err != nil {
		return err
	}
	for _, checkout := range checkouts {
		key := identity(checkout)
		if w.seen[key] || identity(leadHome(w.ctx, checkout)) != w.home {
			continue
		}
		w.seen[key] = true
		w.out = append(w.out, checkout)
		if err := w.modules(checkout); err != nil {
			return err
		}
	}
	return nil
}

// duologModule reports whether dir is an initialized submodule of checkout
// (its own checkout root, whose superproject is checkout) with the walk's
// lead home. An uninitialized module is an empty folder of checkout.
func (w *submoduleWalk) duologModule(checkout, dir string) bool {
	if !hasLeadConfig(dir) {
		return false
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	top := gitRootContext(w.ctx, dir, "--show-toplevel")
	if top == "" || identity(top) != identity(dir) {
		return false
	}
	parent := superproject(w.ctx, dir)
	return parent != "" && identity(parent) == identity(checkout) && identity(leadHome(w.ctx, dir)) == w.home
}

// modulePaths reads the submodule paths of checkout's .gitmodules from Git's
// NUL-delimited config records. A missing file lists none; a path that is
// absolute or leaves the checkout is not taken.
func (w *submoduleWalk) modulePaths(checkout string) ([]string, error) {
	file := filepath.Join(checkout, ".gitmodules")
	if _, err := os.Lstat(file); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	out, err := Git(w.ctx, nil, "config", "-z", "--file", file, "--get-regexp", `^submodule\..*\.path$`)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return nil, nil // no submodule has a path
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w %s", file, err, strings.TrimSpace(GitStderr(err)))
	}
	return parseModulePaths(file, string(out))
}

// parseModulePaths reads `git config -z --get-regexp` output: each record is
// the key, a newline and the value, ended by NUL.
func parseModulePaths(file, out string) ([]string, error) {
	var paths []string
	for _, record := range strings.Split(out, "\x00") {
		if record == "" {
			continue
		}
		_, value, ok := strings.Cut(record, "\n")
		if !ok {
			return nil, fmt.Errorf("%s: malformed submodule record %q", file, record)
		}
		if value == "" || pathutil.Outside(value) {
			continue
		}
		paths = append(paths, value)
	}
	return paths, nil
}

// identity is the resolved, cleaned spelling of a path, for comparisons.
func identity(path string) string {
	return pathutil.Clean(pathutil.Resolved(path))
}
