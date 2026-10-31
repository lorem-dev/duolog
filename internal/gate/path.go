package gate

import "path/filepath"

// RelativeTarget compares real filesystem roots, including OS aliases such as
// /var and /private/var, and parents of files that have not been created yet.
func RelativeTarget(root, cwd, target string) string {
	if cwd == "" {
		cwd = root
	}
	path := filepath.FromSlash(target)
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	root, path = resolvedPath(root), resolvedPath(path)
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}

func resolvedPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	parent := filepath.Dir(path)
	if parent == path {
		return path
	}
	return filepath.Join(resolvedPath(parent), filepath.Base(path))
}
