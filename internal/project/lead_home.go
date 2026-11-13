package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// leadHomes caches lead homes by folder for the life of the process.
var leadHomes sync.Map

// leadHomeResult is a lead home with each root whose config file decided it
// and whether that file was there.
type leadHomeResult struct {
	dir    string
	checks []configCheck
}

type configCheck struct {
	root string
	had  bool
}

// current reports whether every config file that decided r is as it was.
func (r leadHomeResult) current() bool {
	for _, check := range r.checks {
		if Installed(check.root) != check.had {
			return false
		}
	}
	return true
}

// LeadHome returns the .duolog directory that owns lead state for cwd.
// Configured Git submodules share their configured superproject's lead home;
// all other repositories and directories keep their own state directory.
//
// A result is cached while the config files it was found by stay as they
// were, which a stat checks; one found after a Git failure is not cached.
func LeadHome(cwd string) string {
	key, err := filepath.Abs(cwd)
	if err != nil {
		key = cwd
	}
	if value, ok := leadHomes.Load(key); ok {
		if result, ok := value.(leadHomeResult); ok && result.current() {
			return result.dir
		}
	}
	result, err := resolveLeadHome(context.Background(), cwd)
	if err != nil {
		leadHomes.Delete(key)
	} else {
		leadHomes.Store(key, result)
	}
	return result.dir
}

func leadHome(ctx context.Context, cwd string) string {
	result, _ := resolveLeadHome(ctx, cwd)
	return result.dir
}

// resolveLeadHome walks from cwd's state root up through configured
// superprojects. A Git failure ends the walk at the last root found, with
// the error.
func resolveLeadHome(ctx context.Context, cwd string) (leadHomeResult, error) {
	var r leadHomeResult
	configured := func(root string) bool {
		had := Installed(root)
		r.checks = append(r.checks, configCheck{root: root, had: had})
		return had
	}
	current, err := checkedStateRoot(ctx, cwd)
	seen := make(map[string]bool)
	for err == nil && configured(current) {
		identity, evalErr := filepath.EvalSymlinks(current)
		if evalErr != nil || seen[identity] {
			break
		}
		seen[identity] = true
		var parent string
		if parent, err = superprojectOf(ctx, current, current); err != nil || parent == "" {
			break
		}
		if parent, err = checkedStateRoot(ctx, parent); err != nil || !configured(parent) {
			break
		}
		current = parent
	}
	r.dir = filepath.Join(current, ".duolog")
	return r, err
}

// Installed reports whether duolog was installed in the project at root: its
// .duolog/config.yaml is a file. A project without one is none of duolog's,
// even where the plugin runs for every folder or a .duolog folder is left.
func Installed(root string) bool {
	info, err := os.Stat(filepath.Join(root, ".duolog", "config.yaml"))
	return err == nil && info.Mode().IsRegular()
}

// CapGroup is the fixed peer-cap group for an existing work root: the
// checkout of the lead home's project that the work root lies in through
// Git's superprojects, so a checkout of that project keeps its own cap and a
// submodule shares the checkout that includes it. The walk stops there and
// never groups by a project above the lead home's. A work root outside the
// lead home's project is grouped by the top checkout above it. A Git failure
// is an error: the caller refuses the pane rather than give it a group of
// its own.
func CapGroup(work, home string) (string, error) {
	if !isDir(work) {
		return "", nil
	}
	ctx, top := context.Background(), identity(filepath.Dir(home))
	seen := map[string]bool{}
	for !seen[identity(work)] {
		seen[identity(work)] = true
		main, err := checkedStateRoot(ctx, work)
		if err != nil {
			return "", fmt.Errorf("cannot find the peer cap group of %s: %w", work, err)
		}
		if identity(main) == top {
			return identity(work), nil
		}
		parent, err := superprojectOf(ctx, main, work)
		if err != nil {
			return "", fmt.Errorf("cannot find the peer cap group of %s: %w", work, err)
		}
		if parent == "" {
			break
		}
		work = parent
	}
	return identity(work), nil
}
