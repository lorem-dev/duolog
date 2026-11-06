package gate

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// globNames and globTime bound the expansion of the globs of one command
// line: the names it may look at, matches among them, and the time it may
// take. Symlinks back to their own folder make an expansion grow with every
// segment, and the hook must answer before the agent's timeout.
const (
	globNames = 10000
	globTime  = 500 * time.Millisecond
)

// globBudget is what is left of the bounds for one command line.
type globBudget struct {
	names    int
	deadline time.Time
}

func newGlobBudget() *globBudget {
	return &globBudget{names: globNames, deadline: time.Now().Add(globTime)}
}

// spend takes n names from the budget and reports whether the budget holds.
func (b *globBudget) spend(n int) bool {
	b.names -= n
	return b.names >= 0 && time.Now().Before(b.deadline)
}

// glob is filepath.Glob within the budget; ok is false when it ran out
// before the expansion ended.
func (b *globBudget) glob(pattern string) (matches []string, ok bool) {
	if _, err := filepath.Match(pattern, ""); err != nil {
		return nil, true
	}
	if !globMeta(pattern) {
		if !b.spend(1) {
			return nil, false
		}
		if _, err := os.Lstat(pattern); err != nil {
			return nil, true
		}
		return []string{pattern}, true
	}
	dir, file := filepath.Split(pattern)
	dir = globFolder(dir)
	dirs := []string{dir}
	if globMeta(dir[len(filepath.VolumeName(dir)):]) {
		if dir == pattern {
			return nil, true
		}
		if dirs, ok = b.glob(dir); !ok {
			return nil, false
		}
	}
	for _, dir := range dirs {
		names, ok := b.readNames(dir)
		if !ok {
			return nil, false
		}
		for _, name := range names {
			if matched, _ := filepath.Match(file, name); matched {
				matches = append(matches, filepath.Join(dir, name))
			}
		}
	}
	return matches, true
}

// readNames lists the names in a folder, no more than the budget holds. A
// path that is not a folder has none.
func (b *globBudget) readNames(dir string) ([]string, bool) {
	if !b.spend(1) {
		return nil, false
	}
	f, err := os.Open(dir)
	if err != nil {
		return nil, true
	}
	defer f.Close()
	names, _ := f.Readdirnames(max(b.names, 0) + 1)
	return names, b.spend(len(names))
}

// globMeta is filepath's hasMeta: a backslash escapes outside Windows.
func globMeta(pattern string) bool {
	magic := `*?[\`
	if runtime.GOOS == "windows" {
		magic = `*?[`
	}
	return strings.ContainsAny(pattern, magic)
}

// globFolder is filepath's cleanGlobPath: the folder part of a pattern
// without its last separator.
func globFolder(dir string) string {
	volume := filepath.VolumeName(dir)
	switch {
	case dir == "":
		return "."
	case dir == volume:
		return dir + "."
	case len(dir) == len(volume)+1 && os.IsPathSeparator(dir[len(dir)-1]):
		return dir
	default:
		return dir[:len(dir)-1]
	}
}
