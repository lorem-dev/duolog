package gate

import (
	"github.com/lorem-dev/duolog/internal/pathutil"
	"path/filepath"
	"runtime"
)

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
