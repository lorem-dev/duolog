package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// This file holds how install finds the project skills folders where an
// earlier install put duolog's skills: the one a config key names, and the
// default one, without following a link.

// defaultSkillDir returns the default skills folder of a, when the folder that
// SkillDir names is another one; otherwise "". Two names for one directory,
// such as a link or a spelling that differs only in case on a file system that
// ignores it, are one folder, and "" is the answer for them too, so that the
// folder is not looked at twice.
func defaultSkillDir(a Agent, o Options, base string) string {
	d, ok := a.Installer.(SkillDefaults)
	if !ok {
		return ""
	}
	old := d.DefaultSkillDir(o.Scope, base, o.Home)
	if old == "" || sameDir(old, a.Installer.SkillDir(o.Scope, base, o.Home)) {
		return ""
	}
	return old
}

// sameDir reports whether the two paths are the same directory.
func sameDir(a, b string) bool {
	x, err := os.Stat(a)
	if err != nil {
		return false
	}
	y, err := os.Stat(b)
	return err == nil && os.SameFile(x, y)
}

// firstLink returns the first folder from base down to path that is a link, or
// "" when there is none. A folder that does not exist ends the search.
func firstLink(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	dir := base
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		dir = filepath.Join(dir, part)
		info, err := os.Lstat(dir)
		if err != nil {
			return ""
		}
		if isLinkMode(info.Mode()) {
			return dir
		}
	}
	return ""
}

// isLinkMode reports whether a file mode is a link. Go reports a symbolic link
// as ModeSymlink, and a Windows junction, which is a reparse point of another
// kind, as ModeIrregular.
func isLinkMode(m os.FileMode) bool { return m&(os.ModeSymlink|os.ModeIrregular) != 0 }

// skipSkill says whether install leaves the project skill folder alone: it
// does not exist, which needs no note, or it is, or sits below, a link, which
// does, because the link may lead out of the project.
func skipSkill(skill, base string) (note string, skip bool) {
	if _, err := os.Lstat(skill); err != nil {
		return "", true
	}
	if link := firstLink(base, skill); link != "" {
		return fmt.Sprintf("left %s: %s is a link", skill, link), true
	}
	return "", false
}

// pruneTree removes the empty folders under dir and dir itself when it is
// empty. A folder with a file in it stays, and a link is never removed.
func pruneTree(dir string) {
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() || isLinkMode(info.Mode()) {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			pruneTree(filepath.Join(dir, e.Name()))
		}
	}
	_ = os.Remove(dir)
}
