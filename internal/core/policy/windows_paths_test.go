package policy

import "path/filepath"

// absoluteTestPath gives a slash-rooted fixture the native volume on Windows.
// Unlike /other alone, C:/other is an absolute path for filepath.IsAbs there.
func absoluteTestPath(root, target string) string {
	return filepath.ToSlash(filepath.VolumeName(root)) + target
}
