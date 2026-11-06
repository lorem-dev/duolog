package gate

import (
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lorem-dev/duolog/internal/pathutil"
)

// workTarget places a path that a command in a names, as written, from a's
// work root, as RelativeTarget does.
func workTarget(a Action, target string) string {
	if a.Work == "" {
		return target
	}
	return RelativeTarget(a.Work, a.Cwd, expandHome(target))
}

// posixShell reports whether a's command runs in a POSIX shell on a POSIX
// host, where a backslash is never a path separator. PowerShell, cmd and any
// shell on Windows read it as one.
func posixShell(a Action) bool {
	return runtime.GOOS != "windows" && a.Shell != "powershell" && a.Shell != "windows"
}

// literalOutside reports whether target, a command word, names a path outside
// a's work root when its backslashes are characters of a file name. A work
// root or folder written as a Windows path keeps RelativeTarget's reading. A
// drive letter still names a Windows root.
func literalOutside(a Action, target string) bool {
	if a.Work != "" {
		cwd := a.Cwd
		if cwd == "" {
			cwd = a.Work
		}
		if pathutil.Windows(a.Work) || pathutil.Windows(cwd) {
			return outside(RelativeTarget(a.Work, a.Cwd, target))
		}
		name := target
		if !filepath.IsAbs(name) {
			name = filepath.Join(cwd, name)
		}
		relative, err := filepath.Rel(pathutil.Resolved(a.Work), literalResolved(name))
		if err != nil {
			return true
		}
		target = relative
	}
	target = path.Clean(target)
	return strings.HasPrefix(target, "/") || len(target) >= 2 && target[1] == ':' || target == ".." || strings.HasPrefix(target, "../") || strings.HasPrefix(target, "~/")
}

// literalResolved is pathutil.Resolved for a POSIX name, whose backslashes
// are characters of a name rather than the mark of a foreign Windows path.
func literalResolved(name string) string {
	if resolved, err := filepath.EvalSymlinks(name); err == nil {
		return resolved
	}
	parent := filepath.Dir(name)
	if parent == name {
		return name
	}
	return filepath.Join(literalResolved(parent), filepath.Base(name))
}

// RelativeTarget compares real filesystem roots, including OS aliases such as
// /var and /private/var, and parents of files that have not been created yet.
func RelativeTarget(root, cwd, target string) string {
	if cwd == "" {
		cwd = root
	}
	// Foreign Windows paths cannot be resolved by the host filesystem.
	if runtime.GOOS != "windows" && (pathutil.Windows(root) || pathutil.Windows(cwd) || pathutil.IsAbs(target) && pathutil.Windows(target)) {
		target = pathutil.Clean(target)
		if !pathutil.IsAbs(target) {
			target = pathutil.Clean(pathutil.Slashes(cwd) + "/" + target)
		}
		rel, err := pathutil.Relative(root, target)
		if err != nil {
			return target
		}
		return rel
	}
	path := pathutil.Native(target)
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	root, path = pathutil.Resolved(root), pathutil.Resolved(path)
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return pathutil.Clean(path)
	}
	return pathutil.Clean(relative)
}
