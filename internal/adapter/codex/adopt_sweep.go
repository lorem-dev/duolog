package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/install"
	"github.com/lorem-dev/duolog/internal/store"
)

// sweepTimeout bounds AdoptWorktrees, which a lead's session start runs.
const sweepTimeout = 4 * time.Second

// AdoptWorktrees gives the linked worktrees of the Git repositories at or
// below the project root root the Codex trust of their main checkout, as
// AdoptWorktree does for one, and tidies up after removed ones, for a Codex
// process whose environment is environ. A worktree made with plain git
// worktree add then gets trust before duolog delegates into it.
//
// The repositories are the main checkouts at or below root that Codex
// trusts in config.toml; each costs one git command, and only a worktree
// that still needs adoption (needsAdoption) goes through AdoptWorktree's
// checks. Then it takes out every block of tables that AdoptWorktree wrote,
// anywhere in config.toml, whose worktree folder no longer exists while the
// folder above it and its source root do (prunable); a block whose folder
// exists, and any table duolog did not write, stays. Last it
// approves again the hooks that duolog's install made stale where the
// person had approved the hook at their place (renewStale). Every write keeps
// AdoptWorktree's lock, backup and single-rename rules. The whole call is
// bounded by sweepTimeout. The lines say what it wrote; none means nothing
// changed. Failures say nothing but the error.
func (Trust) AdoptWorktrees(root string, environ []string) ([]string, error) {
	if !plainPath(root) || !filepath.IsAbs(root) {
		return nil, nil
	}
	codexHome, home, ok := codexHomes(environ)
	if !ok {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), sweepTimeout)
	defer cancel()
	config := configPath(codexHome)
	data, err := store.ReadFile(config)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lines, errs := adoptBelow(ctx, filepath.Clean(root), data, environ)
	for _, step := range []func() ([]string, error){
		func() ([]string, error) { return pruneRemoved(ctx, config) },
		func() ([]string, error) { return renewStale(ctx, config, home, environ) },
	} {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		more, err := step()
		lines = append(lines, more...)
		errs = append(errs, err)
	}
	return lines, errors.Join(errs...)
}

// adoptBelow runs AdoptWorktree for each linked worktree that needs it of
// each repository that trustedMains finds in the config data.
func adoptBelow(ctx context.Context, root string, data []byte, environ []string) ([]string, []error) {
	var lines []string
	var errs []error
	for _, main := range trustedMains(data, root) {
		if ctx.Err() != nil {
			return lines, append(errs, ctx.Err())
		}
		top, entries, ok := mainCheckout(ctx, main)
		if !ok || !samePath(top, main) {
			continue
		}
		for _, e := range entries[1:] {
			if e.bare || e.prunable || !plainPath(e.path) || !filepath.IsAbs(e.path) || !needsAdoption(e.path, environ) {
				continue
			}
			more, err := adoptWorktree(ctx, main, filepath.Clean(e.path), environ)
			lines = append(lines, more...)
			errs = append(errs, err)
		}
	}
	return lines, errs
}

// trustedMains lists the folders at or below root that Codex trusts in the
// config data, each once, in key order. Whether each is a main checkout is
// for git worktree list to tell.
func trustedMains(data []byte, root string) []string {
	var cfg adoptConfig
	if toml.Unmarshal(data, &cfg) != nil {
		return nil
	}
	var mains []string
	seen := map[string]bool{}
	for _, key := range sortedKeys(cfg.Projects) {
		if cfg.Projects[key]["trust_level"] != "trusted" || !plainPath(key) || !filepath.IsAbs(key) || !atOrBelow(root, key) {
			continue
		}
		id := pathutil.Clean(pathutil.Resolved(key))
		if !seen[id] {
			seen[id] = true
			mains = append(mains, filepath.Clean(key))
		}
	}
	return mains
}

// atOrBelow reports whether p is root or lies below it, both resolved.
func atOrBelow(root, p string) bool {
	rel, err := pathutil.Relative(pathutil.Clean(pathutil.Resolved(root)), pathutil.Clean(pathutil.Resolved(p)))
	return err == nil && !pathutil.Outside(rel)
}

// pruneRemoved takes out of the config file at config every block of tables
// that AdoptWorktree wrote for a worktree whose folder no longer exists.
func pruneRemoved(ctx context.Context, config string) ([]string, error) {
	return adopter{ctx: ctx, path: config}.change(func(data []byte) (configChange, bool, error) {
		edited, gone := pruneBlocks(data)
		if len(gone) == 0 {
			return configChange{}, false, nil
		}
		return configChange{edited: edited, lines: func(path string) []string {
			out := make([]string, 0, len(gone))
			for _, dir := range gone {
				out = append(out, fmt.Sprintf("Codex no longer trusts %s, a linked worktree that no longer exists: duolog took out the tables it wrote for it (%s)", dir, path))
			}
			return out
		}}, true, nil
	})
}

// pruneBlocks cuts out of data duolog's blocks for each removed worktree
// (removedWorktrees), one worktree at a time, each cut checked as
// withdrawal.apply checks it. gone names the worktrees whose blocks went.
func pruneBlocks(data []byte) (edited []byte, gone []string) {
	edited = data
	for _, dir := range removedWorktrees(data) {
		var cfg adoptConfig
		if toml.Unmarshal(edited, &cfg) != nil {
			return nil, nil
		}
		blocks := duologBlocks(edited, dir, cfg.Hooks.State)
		if len(blocks) == 0 || !prunable(dir, blocks) {
			continue
		}
		next, ok := withdrawal{dir: dir, from: blocks[0].from, blocks: blocks}.apply(edited)
		if !ok {
			continue
		}
		edited = next
		gone = append(gone, dir)
	}
	return edited, gone
}

// prunable reports whether the blocks for the removed worktree dir may go:
// each table names dir spelled byte for byte as the comment does, so no
// cleaning of a path such as /a/b/../c decides it, and the folder above dir
// and each block's source root still exist. A worktree on a volume that is
// not mounted keeps its tables.
func prunable(dir string, blocks []duologBlock) bool {
	if !isDir(filepath.Dir(dir)) {
		return false
	}
	hooksFile := filepath.Join(dir, ".codex", "hooks.json")
	for _, b := range blocks {
		if !isDir(b.from) || b.trustKey != "" && b.trustKey != dir {
			return false
		}
		for _, a := range b.approvals {
			if path, _, _, ok := splitApproval(a.key); !ok || path != hooksFile {
				return false
			}
		}
	}
	return true
}

// isDir reports whether path is a folder now.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// removedWorktrees lists the worktrees that duolog's comments in data name
// and that do not exist, each once, in order. A comment that could name
// several folders names each of them: duologBlocks then takes only the tables
// that hold what AdoptWorktree wrote for that very folder.
func removedWorktrees(data []byte) []string {
	var dirs []string
	seen := map[string]bool{}
	for _, l := range configLines(data) {
		body, prefixed := strings.CutPrefix(l.text, commentPrefix)
		body, suffixed := strings.CutSuffix(body, commentSuffix)
		if !prefixed || !suffixed {
			continue
		}
		for i := 0; i < len(body); {
			k := strings.Index(body[i:], commentMiddle)
			if k < 0 {
				break
			}
			k += i
			i = k + 1
			dir, from := body[:k], body[k+len(commentMiddle):]
			if !plainPath(dir) || !filepath.IsAbs(dir) || dir != filepath.Clean(dir) || !plainPath(from) || !filepath.IsAbs(from) || seen[dir] {
				continue
			}
			if _, err := os.Lstat(dir); os.IsNotExist(err) {
				seen[dir] = true
				dirs = append(dirs, dir)
			}
		}
	}
	return dirs
}

// SweepCovers reports whether AdoptWorktrees for the project root root would
// give dir Codex trust now: dir is a linked worktree of a main checkout at or
// below root, proven as AdoptWorktree proves it, and the adoption's checks
// pass. It writes nothing.
func (Trust) SweepCovers(root, dir string, environ []string) bool {
	if !plainPath(root) || !plainPath(dir) || !filepath.IsAbs(root) || !filepath.IsAbs(dir) || !needsAdoption(dir, environ) {
		return false
	}
	codexHome, home, ok := codexHomes(environ)
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), sweepTimeout)
	defer cancel()
	dir = filepath.Clean(dir)
	main, _, ok := mainCheckout(ctx, dir)
	if !ok || samePath(main, dir) || !atOrBelow(root, main) {
		return false
	}
	if _, ok := linkedWorktreeCtx(ctx, main, dir); !ok {
		return false
	}
	data, err := store.ReadFile(configPath(codexHome))
	if err != nil {
		return false
	}
	a := adopter{dir: dir, roots: []string{main}, userHooks: filepath.Join(codexHome, "hooks.json"), stale: install.StaleApprovalsIn(home, environ)}
	_, ok = planAdoption(data, a.dir, a.roots, a.userHooks, a.stale)
	return ok && a.adoptionProblem(data) == ""
}
