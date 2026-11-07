// Package project separates shared state from the current working tree.
package project

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func gitRoot(cwd, flag string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", flag)
	cmd.Stdin = strings.NewReader("")
	data, err := cmd.Output()
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(string(data))
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	path = filepath.Clean(path)
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return ""
	}
	return path
}

// mainWorktree returns the main checkout of the repository whose shared Git
// folder is common, from its core.worktree setting: a submodule's folder sits
// outside its checkout and says where it is. A bare repository sets none, and
// "" is returned for it or when the setting cannot be read.
func mainWorktree(common string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "config", "--file", filepath.Join(common, "config"), "--get", "core.worktree")
	cmd.Stdin = strings.NewReader("")
	data, err := cmd.Output()
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(string(data))
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(common, path)
	}
	path = filepath.Clean(path)
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return ""
	}
	return path
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

// StateRoot returns the main checkout for shared .duolog state.
func StateRoot(cwd string) string {
	common := gitRoot(cwd, "--git-common-dir")
	if common == "" {
		return cwd
	}
	// A submodule's common folder is <parent>/.git/modules/<name>, which is no
	// checkout. Only a folder named .git has its checkout beside it; for any
	// other, Git names the main checkout itself, which a linked worktree of the
	// submodule shares. A bare repository has none, so its worktrees stand alone.
	if filepath.Base(common) != ".git" {
		if main := mainWorktree(common); main != "" {
			return preserveRoot(cwd, main)
		}
		if top := gitRoot(cwd, "--show-toplevel"); top != "" {
			return preserveRoot(cwd, top)
		}
		return cwd
	}
	return preserveRoot(cwd, filepath.Dir(common))
}

func CurrentWorkRoot(fallback string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return fallback
	}
	return WorkRoot(cwd, fallback)
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
