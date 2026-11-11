// Package project separates shared state from the current working tree.
package project

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
)

// gitTimeout bounds one Git command that places a folder.
const gitTimeout = 2 * time.Second

// GitError is a Git command that failed to run, failed or timed out, told
// apart from an answer that a folder lies outside Git or has no relation.
type GitError struct {
	Args []string
	Err  error
}

func (e *GitError) Error() string {
	return "git " + strings.Join(e.Args, " ") + ": " + e.Err.Error()
}

func (e *GitError) Unwrap() error { return e.Err }

// gitOutput runs git with args for at most gitTimeout and returns its
// trimmed output. The folder says which checkout this is, not GIT_DIR or
// GIT_WORK_TREE from the caller, as a Git hook exports them.
func gitOutput(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = configfile.CleanGitEnv(os.Environ())
	cmd.Stdin = strings.NewReader("")
	data, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("%w after %s: %w", ctx.Err(), gitTimeout, err)
		}
		return "", &GitError{Args: args, Err: err}
	}
	return strings.TrimSpace(string(data)), nil
}

func gitRoot(cwd, flag string) string {
	return gitRootContext(context.Background(), cwd, flag)
}

func gitRootContext(ctx context.Context, cwd, flag string) string {
	path, _ := gitPath(ctx, cwd, flag)
	return path
}

// gitPath is the folder git rev-parse names for flag in cwd, or "" when that
// is no folder.
func gitPath(ctx context.Context, cwd, flag string) (string, error) {
	path, err := gitOutput(ctx, "-C", cwd, "rev-parse", flag)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	path = filepath.Clean(path)
	if !isDir(path) {
		return "", nil
	}
	return path, nil
}

// isDir reports whether path is a folder that can be read now. A missing or
// unreadable path is no folder, not an error: callers read it as "no
// checkout there".
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// mainWorktree returns the main checkout of the repository whose shared Git
// folder is common, from its core.worktree setting: a submodule's folder sits
// outside its checkout and says where it is. A bare repository sets none, and
// "" is returned for it; an error says that the setting could not be read.
func mainWorktree(ctx context.Context, common string) (string, error) {
	path, err := gitOutput(ctx, "config", "--file", filepath.Join(common, "config"), "--get", "core.worktree")
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", nil // the setting is not there
	}
	if err != nil || path == "" {
		return "", err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(common, path)
	}
	path = filepath.Clean(path)
	if !isDir(path) {
		return "", nil
	}
	return path, nil
}

// mainCheckout reports whether dir is the main checkout of a repository by its
// own .git folder, which Git would name as its shared folder.
func mainCheckout(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git", "HEAD"))
	return err == nil && info.Mode().IsRegular()
}

// insideGit reports whether dir or a folder above it has a .git entry, so
// that a failed Git command there is a failure and not a folder outside Git.
func insideGit(dir string) bool {
	dir, err := filepath.Abs(dir)
	return err != nil || gitEntryAt(dir)
}

// gitAbove reports whether a folder above dir has a .git entry: a submodule's
// superproject is always one, so without it dir is no submodule. A folder
// that cannot be placed counts as having one, so Git is asked.
func gitAbove(dir string) bool {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return true
	}
	parent := filepath.Dir(dir)
	return parent != dir && gitEntryAt(parent)
}

// gitEntryAt reports whether the absolute folder dir or one above it has a
// .git entry.
func gitEntryAt(dir string) bool {
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// WorkRoot returns the checkout containing cwd, or fallback outside Git.
func WorkRoot(cwd, fallback string) string {
	if root := gitRoot(cwd, "--show-toplevel"); root != "" {
		a, ae := os.Stat(root)
		b, be := os.Stat(fallback)
		if ae == nil && be == nil && os.SameFile(a, b) {
			return fallback
		}
		return preserveRoot(cwd, root)
	}
	return fallback
}

// StateRoot returns the main checkout for this work project's .duolog state.
func StateRoot(cwd string) string {
	return stateRoot(context.Background(), cwd)
}

func stateRoot(ctx context.Context, cwd string) string {
	root, _ := checkedStateRoot(ctx, cwd)
	return root
}

// CheckedStateRoot is StateRoot with an error when Git failed or timed out
// inside a repository; the root is then StateRoot's fallback. A folder
// outside Git is its own state root and no error.
func CheckedStateRoot(cwd string) (string, error) {
	return checkedStateRoot(context.Background(), cwd)
}

func checkedStateRoot(ctx context.Context, cwd string) (string, error) {
	// A main checkout is found by its .git folder alone, without Git.
	if mainCheckout(cwd) {
		return cwd, nil
	}
	common, err := gitPath(ctx, cwd, "--git-common-dir")
	if err != nil {
		if !insideGit(cwd) {
			return cwd, nil
		}
		return cwd, err
	}
	if common == "" {
		return cwd, nil
	}
	// A submodule's common folder is <parent>/.git/modules/<name>, which is no
	// checkout. Only a folder named .git has its checkout beside it; for any
	// other, Git names the main checkout itself, which a linked worktree of the
	// submodule shares. A bare repository has none, so its worktrees stand alone.
	if filepath.Base(common) == ".git" {
		return preserveRoot(cwd, filepath.Dir(common)), nil
	}
	main, mainErr := mainWorktree(ctx, common)
	if main != "" {
		return preserveRoot(cwd, main), nil
	}
	top, err := gitPath(ctx, cwd, "--show-toplevel")
	if top == "" {
		return cwd, errors.Join(mainErr, err)
	}
	return preserveRoot(cwd, top), errors.Join(mainErr, err)
}

// Superproject returns the checkout that includes the repository of cwd as a
// Git submodule, or "" when it is no submodule. A linked worktree of the
// submodule names it too: Git answers only from the submodule's own checkout.
func Superproject(cwd string) string {
	return superproject(context.Background(), cwd)
}

func superproject(ctx context.Context, cwd string) string {
	parent, _ := checkedSuperproject(ctx, cwd)
	return parent
}

// CheckedSuperproject is Superproject with an error when Git failed or timed
// out, told apart from a repository that is no submodule.
func CheckedSuperproject(cwd string) (string, error) {
	return checkedSuperproject(context.Background(), cwd)
}

func checkedSuperproject(ctx context.Context, cwd string) (string, error) {
	main, err := checkedStateRoot(ctx, cwd)
	if err != nil {
		return "", err
	}
	return superprojectOf(ctx, main, cwd)
}

// superprojectOf is the superproject of the repository whose main checkout
// is main, spelled as cwd's ancestors are. Without a .git entry above main it
// is none, and Git is not asked.
func superprojectOf(ctx context.Context, main, cwd string) (string, error) {
	if !gitAbove(main) {
		return "", nil
	}
	path, err := gitOutput(ctx, "-C", main, "rev-parse", "--show-superproject-working-tree")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		return "", nil
	}
	if !isDir(path) {
		return "", nil
	}
	return preserveRoot(cwd, filepath.Clean(path)), nil
}

func CurrentWorkRoot(fallback string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return fallback
	}
	return WorkRoot(cwd, fallback)
}

// LeadRoot returns the checkout whose agent trust a worker in target starts
// from: the checkout this command runs in, or fallback, the project's main
// checkout, when the command runs inside target, where a folder would be
// asked to give trust to itself.
func LeadRoot(fallback, target string) string {
	return leadRoot(CurrentWorkRoot(fallback), fallback, target)
}

func leadRoot(current, fallback, target string) string {
	a, ae := os.Stat(current)
	b, be := os.Stat(target)
	if ae == nil && be == nil && os.SameFile(a, b) {
		return fallback
	}
	return current
}

// Preserve the spelling of cwd's ancestors on systems with filesystem aliases.
func preserveRoot(cwd, root string) string {
	target, err := os.Stat(root)
	if err != nil {
		return root
	}
	for dir := filepath.Clean(cwd); ; dir = filepath.Dir(dir) {
		if info, err := os.Stat(dir); err == nil && os.SameFile(info, target) {
			return dir
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	return root
}
