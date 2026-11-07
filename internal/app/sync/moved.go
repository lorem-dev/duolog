package agentsync

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// This file holds what sync does when agents.<name>.skills_dir moves a target
// skills folder: the files an earlier run generated in the old folder are
// stale, and the run removes the ones it owns.

// noLinks reports whether no folder between the project root and path is a
// symbolic link.
func (r *runner) noLinks(path string) bool {
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		if info, err := os.Lstat(parent); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		if parent == r.root || parent == filepath.Dir(parent) {
			return true
		}
	}
}

// safeParent reports whether a stale file at path may be removed: its parents
// are no links, and a file in a current target folder stays inside it.
func (r *runner) safeParent(path string, moved bool) bool {
	if moved {
		return r.noLinks(path)
	}
	return r.within(path)
}

// staleKind says whether the run looks at the lock entry for removal, and
// whether the entry is in a folder that moved. A skill file in a moved folder
// is looked at unless the run leaves skills alone.
func (r *runner) staleKind(key string, e FileEntry) (wanted, moved bool) {
	if _, ok := r.wantKind(key); ok {
		return true, false
	}
	if r.o.AgentsOnly || !r.movedSkill(key, e) {
		return false, false
	}
	return true, true
}

// foldsCase reports whether the file system of the platform ignores case.
func foldsCase() bool { return runtime.GOOS == "darwin" || runtime.GOOS == "windows" }

// hasPrefix is strings.HasPrefix, ignoring case where the file system does.
func hasPrefix(s, prefix string) bool {
	if foldsCase() && len(s) >= len(prefix) {
		return strings.EqualFold(s[:len(prefix)], prefix)
	}
	return strings.HasPrefix(s, prefix)
}

// skillRest returns the path of the lock key's source below the source skills
// folder, when the key is a local path that ends with it.
func (r *runner) skillRest(key string, e FileEntry) (string, bool) {
	rest, ok := strings.CutPrefix(e.Source, r.from.dirs.Skills+"/")
	if !ok || !strings.HasSuffix(key, "/"+rest) || !filepath.IsLocal(filepath.FromSlash(key)) {
		return "", false
	}
	return rest, true
}

// movedRest returns the path of the lock key's source below the source skills
// folder when the key is a skill file that an earlier run generated in a
// skills folder that no target uses now. The key must end with that path, so
// it is a file of this sync and not a file that only sits in an old folder.
func (r *runner) movedRest(key string, e FileEntry) (string, bool) {
	rest, ok := r.skillRest(key, e)
	if !ok {
		return "", false
	}
	for _, t := range r.to {
		if hasPrefix(key, t.dirs.Agents+"/") || hasPrefix(key, t.dirs.Skills+"/") {
			return "", false
		}
	}
	return rest, true
}

// movedSkill reports whether the lock key is a skill file in a moved folder.
func (r *runner) movedSkill(key string, e FileEntry) bool {
	_, ok := r.movedRest(key, e)
	return ok
}

// adoptSameFiles gives the lock entry of a file that sits in an old skills
// folder to the same file under the new folder name. A link, or a spelling
// that differs only in case on a file system that ignores it, makes the old
// and the new name one file, and removing the old name would remove the new.
func (r *runner) adoptSameFiles(lock *Lock) {
	for key, e := range lock.Files {
		rest, ok := r.skillRest(key, e)
		if !ok {
			continue
		}
		for _, t := range r.to {
			other := t.dirs.Skills + "/" + rest
			if other != key && sameFile(r.abs(key), r.abs(other)) {
				lock.Files[other] = e
				delete(lock.Files, key)
			}
		}
	}
}

// sameFile reports whether the two paths are the same file.
func sameFile(a, b string) bool {
	x, err := os.Stat(a)
	if err != nil {
		return false
	}
	y, err := os.Stat(b)
	return err == nil && os.SameFile(x, y)
}

// keepStale reports whether the run leaves the lock entry alone: its file was
// produced again, its source failed, or its source is outside the folders the
// source side reads now.
func (r *runner) keepStale(key string, e FileEntry, produced map[string]string, failed map[string]bool) bool {
	if _, live := produced[key]; live || failed[e.Source] {
		return true
	}
	return r.refuseMovedSource(key, e)
}

// refuseMovedSource reports whether the entry's source is outside the folders
// the source side reads now, and says so in the conflicts. That happens when
// the source side's skills folder changed and the skills were not moved: the
// source is not deleted, the run just cannot find it, so nothing generated
// from it is removed. Once the old source is gone for good, or the generated
// file is gone already, the entry is stale as any other and the run goes on.
func (r *runner) refuseMovedSource(key string, e FileEntry) bool {
	if hasPrefix(e.Source, r.from.dirs.Skills+"/") || hasPrefix(e.Source, r.from.dirs.Agents+"/") {
		return false
	}
	for _, rel := range []string{e.Source, key} {
		if _, err := os.Lstat(r.abs(rel)); err != nil {
			return false
		}
	}
	old := path.Dir(e.Source)
	for _, t := range r.to {
		if rest, ok := strings.CutPrefix(key, t.dirs.Skills+"/"); ok {
			old = strings.TrimSuffix(e.Source, "/"+rest)
		}
	}
	msg := fmt.Sprintf("%s: the %s skills folder changed to %s; move the skills from %s to %s, do not delete them. Nothing generated from them was removed", old, r.from.name, r.from.dirs.Skills, old, r.from.dirs.Skills)
	if !slices.Contains(r.rep.Conflicts, msg) {
		r.rep.Conflicts = append(r.rep.Conflicts, msg)
	}
	return true
}

// prune removes the folders that a removed file left empty.
func (r *runner) prune(dir string, moved bool) {
	if moved {
		r.pruneMoved(dir)
	} else {
		r.pruneEmpty(dir)
	}
}

// pruneMoved removes the folders an old skills folder leaves empty, from dir up
// to, but not including, the project root. A folder with any file in it stays.
func (r *runner) pruneMoved(dir string) {
	root := filepath.Clean(r.root)
	for dir != root && strings.HasPrefix(dir, root+string(filepath.Separator)) {
		if os.Remove(dir) != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}
