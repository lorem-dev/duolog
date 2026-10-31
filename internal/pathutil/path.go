// Package pathutil compares hook paths independently of the host OS. Agent
// input and committed policy files can contain Windows paths on any host.
package pathutil

import (
	"fmt"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

func Windows(s string) bool {
	return strings.Contains(s, `\`) || len(s) >= 2 && s[1] == ':' || strings.HasPrefix(s, "//")
}

func Slashes(s string) string { return strings.ReplaceAll(s, `\`, "/") }

// Clean preserves POSIX case. Windows drive paths, UNC paths and paths written
// with backslashes compare without case, even in tests on a POSIX host.
func Clean(s string) string {
	win := Windows(s) || runtime.GOOS == "windows"
	s = Slashes(s)
	switch {
	case len(s) >= 3 && s[1] == ':' && s[2] == '/':
		// A drive is a root, not a removable path component (C:/../x).
		s = s[:2] + path.Clean(s[2:])
	case strings.HasPrefix(s, "//"):
		parts := strings.Split(strings.TrimPrefix(s, "//"), "/")
		if len(parts) >= 2 {
			s = "//" + parts[0] + "/" + parts[1] + path.Clean("/"+strings.Join(parts[2:], "/"))
			s = strings.TrimSuffix(s, "/")
		} else {
			s = "/" + path.Clean(s)
		}
	default:
		s = path.Clean(s)
	}
	if win {
		s = strings.ToLower(s)
	}
	return s
}

func IsAbs(s string) bool {
	s = Slashes(s)
	return strings.HasPrefix(s, "/") || len(s) >= 2 && s[1] == ':'
}

func Outside(s string) bool {
	s = Clean(s)
	return IsAbs(s) || s == ".." || strings.HasPrefix(s, "../") || strings.HasPrefix(s, "~/")
}

// Relative handles foreign drive and UNC paths lexically; native callers can
// resolve symlinks before comparing. Different drives must remain outside.
func Relative(root, target string) (string, error) {
	if Windows(root) || Windows(target) || runtime.GOOS == "windows" {
		root, target = strings.ToLower(Clean(root)), strings.ToLower(Clean(target))
		if volume(root) != volume(target) {
			return "", fmt.Errorf("different path volumes")
		}
		a, b := strings.Split(strings.TrimSuffix(root, "/"), "/"), strings.Split(strings.TrimSuffix(target, "/"), "/")
		i := 0
		for i < len(a) && i < len(b) && a[i] == b[i] {
			i++
		}
		parts := make([]string, len(a)-i)
		for j := range parts {
			parts[j] = ".."
		}
		parts = append(parts, b[i:]...)
		if len(parts) == 0 {
			return ".", nil
		}
		return strings.Join(parts, "/"), nil
	}
	rel, err := filepath.Rel(root, target)
	return filepath.ToSlash(rel), err
}

func volume(s string) string {
	if len(s) >= 2 && s[1] == ':' {
		return s[:2]
	}
	if strings.HasPrefix(s, "//") {
		parts := strings.Split(strings.TrimPrefix(s, "//"), "/")
		if len(parts) >= 2 {
			return "//" + parts[0] + "/" + parts[1]
		}
	}
	return ""
}

func Native(s string) string { return filepath.FromSlash(Slashes(s)) }

// Resolved expands filesystem aliases (including Windows 8.3 names) in the
// existing prefix, preserving a nonexistent suffix such as a new file or glob.
// Foreign Windows paths remain lexical on POSIX hosts.
func Resolved(name string) string {
	if runtime.GOOS != "windows" && Windows(name) {
		return name
	}
	if resolved, err := filepath.EvalSymlinks(name); err == nil {
		return resolved
	}
	parent := filepath.Dir(name)
	if parent == name {
		return name
	}
	return filepath.Join(Resolved(parent), filepath.Base(name))
}
