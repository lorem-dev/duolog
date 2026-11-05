package telemetry

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Protection reports the checks on the telemetry root for status and doctor.
// Refusal is a closed name, never a path.
type Protection struct {
	ModeFixed      bool // a too open root was tightened
	BackupVerified bool // the backup exclusion was written and read back
	IndexVerified  bool // the root is kept out of the search index
	Refusal        string
}

const (
	refusalNone        = "none"
	refusalSymlink     = "symlink"
	refusalOwner       = "owner"
	refusalGit         = "git"
	refusalSync        = "sync"
	refusalNFS         = "nfs"
	refusalPermissions = "permissions"
	refusalOther       = "other"
)

var (
	errBadName = errors.New("telemetry: invalid file name")
	errUnsafe  = errors.New("telemetry: unsafe file")
	errOwner   = errors.New("telemetry: foreign owner")
	errNFS     = errors.New("telemetry: root on NFS")
)

// refusedError names the refusal and nothing else, so an error printed by
// doctor carries no path.
type refusedError struct{ refusal string }

func (e *refusedError) Error() string { return "telemetry: root refused: " + e.refusal }

// raceHook lets tests replace files between a check and an open.
var raceHook func(stage string)

func race(stage string) {
	if raceHook != nil {
		raceHook(stage)
	}
}

// fileName is every name opened under the root. Days are checked again as
// dates; ids are the 16 hex characters of the project id.
var fileName = regexp.MustCompile(`^(?:salt|prune\.stamp|CACHEDIR\.TAG|v1/(?:_account|[0-9a-f]{16})/(?:telemetry\.lock|([0-9]{4}-[0-9]{2}-[0-9]{2})\.ndjson(?:\.gz(?:\.tmp)?)?))$`)

// syncedFolders are cloud-synced folder names, matched as whole components
// or with the suffix their business plans add ("OneDrive - Example").
var syncedFolders = []string{"Dropbox", "OneDrive", "Google Drive"}

func refuse(p Protection, refusal string) (Protection, error) {
	p.Refusal = refusal
	return p, &refusedError{refusal: refusal}
}

// openProtected creates the root if needed, checks it and opens it as an
// os.Root. A refused root is never created, and nothing is created inside
// it.
func openProtected(root string) (*os.Root, Protection, error) {
	p := Protection{Refusal: refusalNone}
	root, err := filepath.Abs(root)
	if err != nil {
		return refuseOpen(p, refusalOther)
	}
	if refusal := placement(root); refusal != refusalNone {
		return refuseOpen(p, refusal)
	}
	if err := makeRoot(root); err != nil {
		return refuseOpen(p, errRefusal(err))
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		// The checks name the reason: a symlink, a file, no access.
		if p, err := checkRoot(root); err != nil {
			return nil, p, err
		}
		return refuseOpen(p, errRefusal(err))
	}
	race("root")
	if p, err = checkRoot(root); err != nil {
		r.Close()
		return nil, p, err
	}
	// The handle must be the directory the checks saw.
	if err := sameDir(r, root); err != nil {
		r.Close()
		return refuseOpen(p, refusalOther)
	}
	// An exclusion that fails stays a doctor warning.
	p.BackupVerified, _ = excludeBackup(root)
	p.IndexVerified = indexExcluded(root)
	return r, p, nil
}

// openExisting opens a root that already exists with the checks of
// openProtected and writes nothing: no directory, mode, ACL or backup
// exclusion. A store with collection off opens its root this way.
func openExisting(root string) (*os.Root, Protection, error) {
	p := Protection{Refusal: refusalNone}
	root, err := filepath.Abs(root)
	if err != nil {
		return refuseOpen(p, refusalOther)
	}
	if p, _, err := inspectRoot(root); err != nil {
		return nil, p, err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return refuseOpen(p, errRefusal(err))
	}
	race("root")
	if p, _, err := inspectRoot(root); err != nil {
		r.Close()
		return nil, p, err
	}
	if err := sameDir(r, root); err != nil {
		r.Close()
		return refuseOpen(p, refusalOther)
	}
	return r, p, nil
}

func refuseOpen(p Protection, refusal string) (*os.Root, Protection, error) {
	p, err := refuse(p, refusal)
	return nil, p, err
}

// checkRoot checks an existing root: a directory, not a link, owned by this
// user, outside git trees, synced folders and NFS. Owner and place are
// checked before a too open mode is tightened.
func checkRoot(root string) (Protection, error) {
	p, info, err := inspectRoot(root)
	if err != nil {
		return p, err
	}
	fixed, err := tightenRoot(root, info)
	if err != nil {
		return refuse(p, refusalPermissions)
	}
	p.ModeFixed = fixed
	return p, nil
}

// inspectRoot runs the checks of checkRoot and changes nothing; a mode that
// is merely too open passes.
func inspectRoot(root string) (Protection, os.FileInfo, error) {
	p := Protection{Refusal: refusalNone}
	refused := func(refusal string) (Protection, os.FileInfo, error) {
		p, err := refuse(p, refusal)
		return p, nil, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return refused(errRefusal(err))
	}
	if linked(info) {
		return refused(refusalSymlink)
	}
	if !info.IsDir() {
		return refused(refusalOther)
	}
	if err := secureOwner(root, info); err != nil {
		if errors.Is(err, errOwner) {
			return refused(refusalOwner)
		}
		return refused(errRefusal(err))
	}
	if refusal := placement(root); refusal != refusalNone {
		return refused(refusal)
	}
	return p, info, nil
}

func errRefusal(err error) string {
	if errors.Is(err, fs.ErrPermission) {
		return refusalPermissions
	}
	return refusalOther
}

// placement refuses a root that resolves into a synced folder, a git work
// tree or an NFS mount. Git is found by its marker; Git is never run.
func placement(root string) string {
	resolved, err := resolveExisting(root)
	if err != nil {
		return errRefusal(err)
	}
	if synced(resolved) {
		return refusalSync
	}
	if inGitTree(resolved) {
		return refusalGit
	}
	if err := refuseMount(resolved); err != nil {
		if errors.Is(err, errNFS) {
			return refusalNFS
		}
		return errRefusal(err)
	}
	return refusalNone
}

// resolveExisting resolves the symlinks of the longest existing prefix of p
// and keeps the rest, which does not exist yet.
func resolveExisting(p string) (string, error) {
	p = filepath.Clean(p)
	var rest []string
	for {
		resolved, err := filepath.EvalSymlinks(p)
		if err == nil {
			for i := len(rest) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, rest[i])
			}
			return resolved, nil
		}
		parent := filepath.Dir(p)
		if !errors.Is(err, fs.ErrNotExist) || parent == p {
			return "", err
		}
		rest = append(rest, filepath.Base(p))
		p = parent
	}
}

func synced(p string) bool {
	parts := strings.Split(filepath.ToSlash(p), "/")
	for i, part := range parts {
		if i > 0 && strings.EqualFold(parts[i-1], "Library") &&
			(strings.EqualFold(part, "Mobile Documents") || strings.EqualFold(part, "CloudStorage")) {
			return true
		}
		for _, name := range syncedFolders {
			if strings.EqualFold(part, name) || foldPrefix(part, name+" - ") || foldPrefix(part, name+" (") {
				return true
			}
		}
	}
	return false
}

func foldPrefix(s, prefix string) bool {
	return len(s) > len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// inGitTree looks for a .git directory or worktree marker file in p and
// each of its ancestors.
func inGitTree(p string) bool {
	for {
		if _, err := os.Lstat(filepath.Join(p, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(p)
		if parent == p {
			return false
		}
		p = parent
	}
}

// makeRoot creates the missing directories down to root, each 0700 set
// explicitly, since duolog sets no umask.
func makeRoot(root string) error {
	var missing []string
	for dir := root; ; dir = filepath.Dir(dir) {
		_, err := os.Lstat(dir)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) || filepath.Dir(dir) == dir {
			return err
		}
		missing = append(missing, dir)
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Mkdir(missing[i], 0o700); err != nil {
			if errors.Is(err, fs.ErrExist) {
				continue // another process made it; checkRoot judges the root
			}
			return err
		}
		if err := os.Chmod(missing[i], 0o700); err != nil {
			return err
		}
	}
	return nil
}

// sameDir confirms that r is the directory now at root.
func sameDir(r *os.Root, root string) error {
	opened, err := r.Stat(".")
	if err != nil {
		return scrub(err)
	}
	now, err := os.Lstat(root)
	if err != nil {
		return scrub(err)
	}
	if linked(now) || !now.IsDir() || !os.SameFile(opened, now) {
		return errUnsafe
	}
	return nil
}

// openRootDir opens root and confirms it is the directory info describes.
func openRootDir(root string, info os.FileInfo) (*os.Root, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, scrub(err)
	}
	opened, err := r.Stat(".")
	if err != nil || !os.SameFile(opened, info) {
		r.Close()
		return nil, errUnsafe
	}
	return r, nil
}

// openRegular opens one of the telemetry files under r. Every directory on
// the way and the file itself must be a real directory or a regular file
// with one link, owned by this user; the opened handle must be the file
// that was checked before anything is truncated, written or chmodded. Name
// uses forward slashes.
func openRegular(r *os.Root, name string, flags int) (*os.File, error) {
	return openChecked(r, name, flags, true)
}

// openReading opens a telemetry file read only with every check of
// openRegular and writes nothing: a file or directory that is merely too
// open is read as it is.
func openReading(r *os.Root, name string) (*os.File, error) {
	return openChecked(r, name, os.O_RDONLY, false)
}

// openChecked is openRegular; repair tightens the modes it finds too open.
func openChecked(r *os.Root, name string, flags int, repair bool) (*os.File, error) {
	m := fileName.FindStringSubmatch(name)
	if m == nil || (m[1] != "" && !validDay(m[1])) {
		return nil, errBadName
	}
	create := flags&os.O_CREATE != 0
	if dir := path.Dir(name); dir != "." {
		if err := secureDirs(r, dir, create, repair); err != nil {
			return nil, err
		}
	}
	before, err := r.Lstat(name)
	switch {
	case err == nil:
		if !regular(before) {
			return nil, errUnsafe
		}
	case errors.Is(err, fs.ErrNotExist) && create:
		before = nil
	default:
		return nil, scrub(err)
	}
	race("file")
	// Truncation waits until the handle is known to be the checked file.
	f, err := r.OpenFile(name, flags&^os.O_TRUNC, 0o600)
	if err != nil {
		return nil, scrub(err)
	}
	if err := confirmFile(r, name, f, before, repair); err != nil {
		f.Close()
		return nil, err
	}
	if flags&os.O_TRUNC != 0 && flags&(os.O_WRONLY|os.O_RDWR) != 0 {
		if err := f.Truncate(0); err != nil {
			f.Close()
			return nil, scrub(err)
		}
	}
	return f, nil
}

func validDay(day string) bool {
	_, err := time.Parse(time.DateOnly, day)
	return err == nil
}

// confirmFile checks the opened handle against the name before and after the
// open, then tightens its mode when repair is set.
func confirmFile(r *os.Root, name string, f *os.File, before os.FileInfo, repair bool) error {
	info, err := fileInfo(f)
	if err != nil {
		return scrub(err)
	}
	if !info.Mode().IsRegular() || !singleLink(info) {
		return errUnsafe
	}
	if before != nil && !os.SameFile(unwrap(info), before) {
		return errUnsafe
	}
	after, err := r.Lstat(name)
	if err != nil || !regular(after) || !os.SameFile(unwrap(info), after) {
		return errUnsafe
	}
	if err := ownedFile(f, info); err != nil {
		return err
	}
	if !repair {
		return nil
	}
	return tighten(r, name, f, info)
}

// secureDirs checks each directory of dir, creating missing ones when
// create is set and tightening them when repair is set.
func secureDirs(r *os.Root, dir string, create, repair bool) error {
	parts := strings.Split(dir, "/")
	for i := range parts {
		d := strings.Join(parts[:i+1], "/")
		info, err := r.Lstat(d)
		if errors.Is(err, fs.ErrNotExist) && create {
			if err := r.Mkdir(d, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				return scrub(err)
			}
			info, err = r.Lstat(d)
		}
		if err != nil {
			return scrub(err)
		}
		if linked(info) || !info.IsDir() {
			return errUnsafe
		}
		if err := confirmDir(r, d, info, repair); err != nil {
			return err
		}
	}
	return nil
}

func confirmDir(r *os.Root, name string, info os.FileInfo, repair bool) error {
	f, err := r.Open(name)
	if err != nil {
		return scrub(err)
	}
	defer f.Close()
	opened, err := fileInfo(f)
	if err != nil {
		return scrub(err)
	}
	if !opened.IsDir() || !os.SameFile(unwrap(opened), info) {
		return errUnsafe
	}
	if err := ownedFile(f, opened); err != nil {
		return err
	}
	if !repair {
		return nil
	}
	return tighten(r, name, f, opened)
}

// linked is true for a symlink or any other reparse point.
func linked(info os.FileInfo) bool {
	return info.Mode()&fs.ModeSymlink != 0 || reparse(info)
}

func regular(info os.FileInfo) bool {
	return info.Mode().IsRegular() && !reparse(info)
}

// scrub drops the path from an error and keeps its cause for errors.Is.
func scrub(err error) error {
	if err == nil {
		return nil
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		err = pathErr.Err
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		err = linkErr.Err
	}
	return fmt.Errorf("telemetry: %w", err)
}
