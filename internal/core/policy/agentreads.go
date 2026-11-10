package policy

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/pathutil"
)

// agentSkillRoots names only skill trees, never the agent config folders
// that also hold credentials and session history. Plugin caches have the
// layout <marketplace>/<plugin>/<version>/skills. Use the target to locate
// that tree without walking the cache on every gate call.
//
// Each root passes wide once its links are resolved, so one that a person
// linked to a wide folder grants nothing. Without wide no root is granted.
func agentSkillRoots(wide func(string) bool, target string) []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || wide == nil {
		return nil
	}
	var roots []string
	// Resolving links costs several system calls per path part, more on
	// Windows, so the target is resolved once for both agents.
	resolved := pathutil.Resolved(target)
	for _, root := range []string{filepath.Join(home, ".agent", "skills"), filepath.Join(home, ".agents", "skills")} {
		if !wide(pathutil.Resolved(root)) {
			roots = append(roots, root)
		}
	}
	for _, agent := range []struct{ dir, env string }{{".codex", "CODEX_HOME"}, {".claude", "CLAUDE_CONFIG_DIR"}} {
		dir := os.Getenv(agent.env)
		if dir == "" {
			dir = filepath.Join(home, agent.dir)
		}
		if !filepath.IsAbs(dir) {
			continue
		}
		own := pathutil.Resolved(dir)
		roots = append(roots, linkedConfigSkillRoots(home, own, resolved, wide)...)
		if skills := filepath.Join(dir, "skills"); ownOrNarrow(wide, pathutil.Resolved(skills), filepath.Join(own, "skills")) {
			roots = append(roots, skills)
		}
		cache := pathutil.Resolved(filepath.Join(dir, "plugins", "cache"))
		if !ownOrNarrow(wide, cache, filepath.Join(own, "plugins", "cache")) {
			continue
		}
		rel, err := filepath.Rel(cache, resolved)
		if err != nil || !filepath.IsLocal(rel) {
			continue
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) >= 4 && samePath(parts[3], "skills") {
			roots = append(roots, filepath.Join(cache, filepath.FromSlash(strings.Join(parts[:4], "/"))))
		}
	}
	return roots
}

// linkedConfigSkillRoots recognizes installed links such as
// .agent/skills/package -> .codex/package/skills. The ordinary install root
// cannot cover that resolved path, and wide refuses all of agent config.
// Exempt only the linked package's skills folder, never its config parent.
// Only a target inside the config folder needs this, so other targets skip
// the folder scans that would otherwise run on every gate call.
func linkedConfigSkillRoots(home, own, resolvedTarget string, wide func(string) bool) []string {
	if !within(own, resolvedTarget) {
		return nil
	}
	var roots []string
	for _, name := range []string{".agent", ".agents"} {
		installed := filepath.Join(home, name, "skills")
		if wide(pathutil.Resolved(installed)) {
			continue
		}
		entries, _ := os.ReadDir(installed)
		for _, entry := range entries {
			if entry.Type()&fs.ModeSymlink == 0 || privateSkillName(entry.Name()) {
				continue
			}
			resolved, err := filepath.EvalSymlinks(filepath.Join(installed, entry.Name()))
			if err == nil && configSkillTree(own, resolved) {
				roots = append(roots, resolved)
			}
		}
	}
	return roots
}

// configSkillTree accepts exactly <agent config>/<package>/skills, with no
// hidden or private package name. Callers pass fully resolved paths, so a
// link to the config folder itself or a protected folder elsewhere fails.
func configSkillTree(own, resolved string) bool {
	rel, err := filepath.Rel(own, resolved)
	if err != nil || !filepath.IsLocal(rel) {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	return len(parts) == 2 && !privateSkillName(parts[0]) && samePath(parts[1], "skills")
}

// ownOrNarrow reports a resolved folder that is where the agent's config
// folder keeps it, or else not wide. The width check refuses everything in an
// agent's config folder, so a folder no link moved is exempt by name: only a
// link can make it mean something else.
func ownOrNarrow(wide func(string) bool, resolved, own string) bool {
	return samePath(resolved, own) || !wide(resolved)
}

// privateSkillFiles are the credential and agent config files a skill tree
// has no reason to hand a worker. Hidden files count without being named.
var privateSkillFiles = []string{
	"auth.json", "credentials", "credentials.json", "secret.json", "secrets.json", "token", "token.json", "tokens.json", "oauth.json",
	"netrc", "id_rsa", "id_dsa", "id_ecdsa", "id_ed25519", "known_hosts", "authorized_keys",
	"mcp.json", "settings.json", "settings.local.json", "hooks.json", "config.toml", "installed_plugins.json",
}

// privateSkillName reports a hidden name, or the name of a credential or
// agent config file.
func privateSkillName(name string) bool {
	return strings.HasPrefix(name, ".") || slices.Contains(privateSkillFiles, strings.ToLower(name))
}

// skillPathReadable is false for a path that names, inside a skill tree once
// links are resolved, a hidden file or a credential or config file. A glob and
// a path that does not exist are left to taskReadPath.
func skillPathReadable(roots []string, target string) bool {
	if glob(target) {
		return true
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return true
	}
	for _, root := range roots {
		real, err := filepath.EvalSymlinks(root)
		if err != nil || !within(real, resolved) {
			continue
		}
		rel, err := filepath.Rel(real, resolved)
		if err != nil || slices.ContainsFunc(strings.Split(filepath.ToSlash(rel), "/"), privateSkillName) {
			return false
		}
	}
	return true
}

// skillSearchable adds to searchable that nothing below a folder is a
// credential or config file. searchable has already bounded the walk.
func skillSearchable(searchable func(string) bool) func(string) bool {
	return func(dir string) bool {
		if !searchable(dir) {
			return false
		}
		return filepath.WalkDir(dir, func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if name != dir && privateSkillName(entry.Name()) {
				return fs.ErrPermission
			}
			return nil
		}) == nil
	}
}

// Reuse task-read containment and recursive-search checks. In particular,
// symlinks may not escape a skill tree, and searches may not reach denied
// files, hidden files or duolog's private stores.
func agentReadTool(a Action, searchable func(string) bool) bool {
	if a.Work == "" {
		return false
	}
	target := physicalJoin(a.Work, pathutil.Native(a.Target))
	a.Reads = agentSkillRoots(a.WideRead, target)
	return skillPathReadable(a.Reads, target) && taskReadTool(a, skillSearchable(searchable))
}

func agentReadWord(a Action, written string, searchable func(string) bool) bool {
	cwd := a.Cwd
	if cwd == "" {
		cwd = a.Work
	}
	target := physicalJoin(cwd, expandHome(written))
	a.Reads = agentSkillRoots(a.WideRead, target)
	return skillPathReadable(a.Reads, target) && taskReadWord(a, written, skillSearchable(searchable))
}
