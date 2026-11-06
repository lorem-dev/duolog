package gate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/pathutil"
	"github.com/lorem-dev/duolog/internal/session"
)

// claudeProject turns a folder into Claude's name for it: every character
// other than an ASCII letter or digit becomes -.
var claudeProject = regexp.MustCompile(`[^A-Za-z0-9]`)

// ClaudeTemps names the temp folders of one Claude session on a POSIX host.
// Claude keeps a session's scratchpad and task output under
// <tmp>/claude-<uid>/<project>/<session>, where <project> is the folder the
// session started in, named as claudeProject names it. The session folder
// holds only that session's files. folders are the candidates for the start
// folder, such as the work root and the hook's folder. tmp is resolved, so
// that /tmp and /private/tmp compare as one; the folders below it are not, so
// a symlink among them leads nowhere InTemp accepts.
func ClaudeTemps(tmp string, uid int, sessionID string, folders ...string) []string {
	if runtime.GOOS == "windows" || tmp == "" || !session.ValidSessionID(sessionID) {
		return nil
	}
	base := filepath.Join(pathutil.Resolved(tmp), fmt.Sprintf("claude-%d", uid))
	var temps []string
	for _, folder := range folders {
		if folder == "" {
			continue
		}
		temp := filepath.Join(base, claudeProject.ReplaceAllString(folder, "-"), sessionID)
		if !slices.Contains(temps, temp) {
			temps = append(temps, temp)
		}
	}
	return temps
}

// InTemp reports whether target, an absolute path, is one of temps or lies
// inside one, once every symlink on the way is resolved. A path with .. in it,
// or with a symlink that leads nowhere, is not: either could leave the folder.
func InTemp(temps []string, target string) bool {
	if len(temps) == 0 || runtime.GOOS == "windows" || !filepath.IsAbs(target) {
		return false
	}
	if slices.Contains(strings.Split(filepath.ToSlash(target), "/"), "..") {
		return false
	}
	resolved, ok := resolvePOSIX(filepath.Clean(target))
	if !ok {
		return false
	}
	return slices.ContainsFunc(temps, func(temp string) bool { return within(temp, resolved) })
}

// within reports whether path is dir or lies inside it, comparing as a POSIX
// host does, where a backslash is part of a name.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../") && !filepath.IsAbs(rel)
}

// resolvePOSIX resolves every symlink in the existing part of name and keeps
// the part that does not exist yet. Unlike pathutil.Resolved it reads a
// backslash as part of a name, as a POSIX host does. It fails for a symlink
// whose target does not exist, since writing to it would create that target
// wherever it is, and for a missing path with .. in it, since only the
// filesystem knows where .. after a symlink leads.
func resolvePOSIX(name string) (string, bool) {
	if resolved, err := filepath.EvalSymlinks(name); err == nil {
		return resolved, true
	}
	if info, err := os.Lstat(name); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", false
	}
	if slices.Contains(strings.Split(filepath.ToSlash(name), "/"), "..") {
		return "", false
	}
	parent := filepath.Dir(name)
	if parent == name {
		return name, true
	}
	resolved, ok := resolvePOSIX(parent)
	return filepath.Join(resolved, filepath.Base(name)), ok
}
