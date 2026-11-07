package policy

import (
	"io/fs"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// InClaudeTemp reports whether target, an absolute path as the worker wrote
// it, is one of temps or lies inside one once every symlink is resolved. A ..
// anywhere in target is refused before anything is cleaned, since after a
// symlink only the filesystem knows where it leads. So is a path that does not
// exist, whose symlinks cannot all be resolved. A folder counts only when no
// symlink below it leads out of its temp folder, since a recursive read such
// as grep -R or rg --follow follows one.
func InClaudeTemp(temps []string, target string) bool {
	if len(temps) == 0 || runtime.GOOS == "windows" || !filepath.IsAbs(target) {
		return false
	}
	if slices.Contains(strings.Split(target, "/"), "..") {
		return false
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return false
	}
	for _, temp := range temps {
		if temp != "" && within(temp, resolved) {
			return staysWithin(temp, resolved)
		}
	}
	return false
}

// within reports whether path is dir or lies inside it, with the separator
// and the case rules of the host.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

// maxTempEntries bounds the walk of staysWithin; a larger folder does not
// count as the worker's own.
const maxTempEntries = 10000

// staysWithin reports whether every symlink at or below path, a resolved path
// inside temp, resolves inside temp, and nothing else there is more than a
// folder or a regular file (specialType): a Windows junction, which Go reads
// as irregular, leads out as a symlink does.
func staysWithin(temp, path string) bool {
	entries := 0
	err := filepath.WalkDir(path, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entries++; entries > maxTempEntries {
			return fs.ErrInvalid
		}
		switch {
		case entry.Type() == fs.ModeSymlink:
			if resolved, err := filepath.EvalSymlinks(name); err != nil || !within(temp, resolved) {
				return fs.ErrPermission
			}
		case specialType(entry.Type()):
			return fs.ErrPermission
		}
		return nil
	})
	return err == nil
}

// ownTempRead reports whether a file that a named read command in a reads, as
// written in the command, lies in the worker's own Claude session temp folder.
func ownTempRead(a Action, target string) bool {
	if len(a.Temp) == 0 {
		return false
	}
	if !filepath.IsAbs(target) {
		cwd := a.Cwd
		if cwd == "" {
			cwd = a.Work
		}
		if cwd == "" {
			return false
		}
		// A plain join keeps every .. for InClaudeTemp to refuse.
		target = cwd + "/" + target
	}
	return InClaudeTemp(a.Temp, target)
}
