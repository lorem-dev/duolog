package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/pathutil"
)

// SameProject reports whether folder is root, a folder below it, or a linked
// worktree that root's repository itself lists. What a folder says about its
// own .git is never taken.
func SameProject(root, folder string) bool {
	if root == "" || folder == "" {
		return false
	}
	root, folder = pathutil.Clean(pathutil.Resolved(root)), pathutil.Clean(pathutil.Resolved(folder))
	if folder == root || strings.HasPrefix(folder, strings.TrimSuffix(root, string(filepath.Separator))+string(filepath.Separator)) {
		return true
	}
	// The folder must be listed by the lead's repository and agree: its own
	// common git directory is the lead's.
	if !slices.Contains(worktrees(root), folder) {
		return false
	}
	common := gitCommonDir(root)
	return common != "" && common == gitCommonDir(folder)
}

// gitCommonDir is the resolved common git directory of the checkout at dir, or
// "" when git cannot say.
func gitCommonDir(dir string) string {
	out, err := cleanGit(dir, "rev-parse", "--git-common-dir")
	common := strings.TrimSuffix(out, "\n")
	if err != nil || common == "" {
		return ""
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	return pathutil.Clean(pathutil.Resolved(common))
}

// worktrees lists the resolved paths of the worktrees that the repository at
// dir lists, or none when git cannot say or dir is no repository root of its
// own: a folder inside an outer repository has that repository's list, which
// is not its project.
func worktrees(dir string) []string {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil
	}
	top, err := cleanGit(dir, "rev-parse", "--show-toplevel")
	if err != nil || pathutil.Clean(pathutil.Resolved(strings.TrimSpace(top))) != pathutil.Clean(pathutil.Resolved(dir)) {
		return nil
	}
	out, err := cleanGit(dir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil
	}
	var trees []string
	for _, path := range parseWorktrees(out) {
		trees = append(trees, pathutil.Clean(pathutil.Resolved(path)))
	}
	return trees
}

// parseWorktrees reads the paths out of `git worktree list --porcelain -z`:
// NUL ends each field and an empty field ends an entry, so a path with a
// newline stays one path. Bare and prunable entries are left out.
func parseWorktrees(list string) []string {
	var paths []string
	path, skip := "", false
	flush := func() {
		if path != "" && !skip {
			paths = append(paths, path)
		}
		path, skip = "", false
	}
	for _, field := range strings.Split(list, "\x00") {
		switch {
		case strings.HasPrefix(field, "worktree "):
			flush()
			path = strings.TrimPrefix(field, "worktree ")
		case field == "bare" || strings.HasPrefix(field, "prunable"):
			skip = true
		}
	}
	flush()
	return paths
}

// cleanGit runs git in dir with the caller's git variables removed.
func cleanGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = cleanGitEnv(os.Environ())
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.Output()
	return string(out), err
}

// cleanGitEnv drops every GIT_ variable, so that git answers for the folder it
// is given and not for a repository the caller's environment points at.
func cleanGitEnv(environ []string) []string {
	clean := make([]string, 0, len(environ))
	for _, kv := range environ {
		if !strings.HasPrefix(strings.ToUpper(kv), "GIT_") {
			clean = append(clean, kv)
		}
	}
	return clean
}
