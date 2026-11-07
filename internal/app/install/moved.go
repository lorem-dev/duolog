package install

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/lorem-dev/duolog/plugin"
)

// This file holds what install does when a config key moves an agent's project
// skills folder: duolog's own skills leave the default folder.

// defaultSkillDir returns the default skills folder of a, when the folder that
// SkillDir names is another one; otherwise "". Two names for one directory,
// such as a link or a spelling that differs only in case on a file system that
// ignores it, are one folder, and "" is the answer for them too, because
// removing duolog's skills from the default name would remove the ones just
// written.
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

// dropDefaultSkills takes duolog's own skills out of the default skills folder
// of every agent whose configured folder is another one, and returns a note
// for each file it removed and each skill it left because of a link.
func dropDefaultSkills(agents []Agent, o Options, base string) ([]string, error) {
	var notes []string
	for _, a := range agents {
		old := defaultSkillDir(a, o, base)
		if old == "" {
			continue
		}
		more, err := dropOwnSkills(old, o, base)
		notes = append(notes, more...)
		if err != nil {
			return notes, err
		}
	}
	return notes, nil
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

// skipSkill says whether dropOwnSkills leaves the skill folder alone: it does
// not exist, which needs no note, or it is, or sits below, a link, which does.
func skipSkill(skill, base string) (note string, skip bool) {
	if _, err := os.Lstat(skill); err != nil {
		return "", true
	}
	if link := firstLink(base, skill); link != "" {
		return fmt.Sprintf("left %s: %s is a link", skill, link), true
	}
	return "", false
}

// dropFile is the step of the walk in dropOwnSkills: it backs up the regular
// file at path and removes it, and reports whether it did. A folder, a link and
// a file that is gone are passed over.
func dropFile(path string, d fs.DirEntry, walkErr error, o Options) (bool, error) {
	if ok, err := dropTarget(d, walkErr); !ok {
		return false, err
	}
	if err := backUp(path, o); err != nil {
		return false, err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return false, err
	}
	return true, nil
}

// dropTarget says whether the walk in dropOwnSkills removes the entry d: a
// regular file. A folder, a link and a file that is gone are passed over.
func dropTarget(d fs.DirEntry, walkErr error) (bool, error) {
	if os.IsNotExist(walkErr) {
		return false, nil
	}
	if walkErr != nil || !d.Type().IsRegular() {
		return false, walkErr
	}
	return true, nil
}

// defaultSkillFiles lists what dropOwnSkills removes from dir, by the same
// rules: every regular file of duolog's bundled skill folders there, except in
// a skill it leaves because of a link. An empty dir has none.
func defaultSkillFiles(dir, base string) ([]string, error) {
	if dir == "" {
		return nil, nil
	}
	var files []string
	for _, name := range plugin.Skills {
		skill := filepath.Join(dir, name)
		if _, skip := skipSkill(skill, base); skip {
			continue
		}
		err := filepath.WalkDir(skill, func(path string, d fs.DirEntry, err error) error {
			ok, err := dropTarget(d, err)
			if ok {
				files = append(files, path)
			}
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

// dropOwnSkills removes every file of duolog's bundled skill folders from dir,
// whatever its content, because an older duolog shipped other files and Codex
// would load both copies. Each file is backed up first, under .duolog/backup
// like the files install replaces, and stays when no backup can be written. No
// other skill is touched. A skill whose folder, or a folder above it, is a link
// is left alone and named in a note, because the link may lead out of the
// project. Folders that end up empty go, from the skill's up to, but not
// including, base, and a link is never removed.
func dropOwnSkills(dir string, o Options, base string) ([]string, error) {
	var notes []string
	for _, name := range plugin.Skills {
		skill := filepath.Join(dir, name)
		note, skip := skipSkill(skill, base)
		if note != "" {
			notes = append(notes, note)
		}
		if skip {
			continue
		}
		err := filepath.WalkDir(skill, func(path string, d fs.DirEntry, err error) error {
			removed, err := dropFile(path, d, err, o)
			if removed {
				notes = append(notes, "removed "+path)
			}
			return err
		})
		if err != nil {
			return notes, err
		}
		pruneTree(skill)
	}
	pruneUp(dir, base)
	return notes, nil
}

// backUp saves the file at path under .duolog/backup of the project, before
// the file goes. It returns an error when no copy of the content can be saved.
func backUp(path string, o Options) error {
	backup := projectBackup(o.Root, path)
	if backup == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(backup), 0o755); err != nil {
		return err
	}
	return saveBackup(backup, data)
}

// saveBackup writes data to backup, or to backup.1, backup.2 and so on when a
// file of that name holds other content. A file that holds the same content is
// the backup already.
func saveBackup(backup string, data []byte) error {
	for i := 0; i < 1000; i++ {
		name := backup
		if i > 0 {
			name = fmt.Sprintf("%s.%d", backup, i)
		}
		f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if os.IsExist(err) {
			have, readErr := os.ReadFile(name)
			if readErr != nil {
				return readErr
			}
			if bytes.Equal(have, data) {
				return nil
			}
			continue
		}
		if err != nil {
			return err
		}
		_, writeErr := f.Write(data)
		if closeErr := f.Close(); writeErr == nil {
			writeErr = closeErr
		}
		return writeErr
	}
	return fmt.Errorf("%s: too many backups of different content", backup)
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
