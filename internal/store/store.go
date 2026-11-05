package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// GitIgnore keeps shared settings in Git and everything a session writes out of it.
const GitIgnore = `# duolog's local state stays out of Git: the bus, sessions, tasks and caches.
*
# Share these with the project.
!.gitignore
!config.yaml
!sync.lock.json
` + CalibrationIgnore

// CalibrationIgnore shares the calibration project part and keeps its runs
// local. duolog install appends it to a .gitignore written before it existed.
const CalibrationIgnore = `# Share the calibration project part; calibration runs stay local.
!calibration/
calibration/*
!calibration/project.yaml
`

type Store struct {
	Dir string
}

// Open creates the project state directory. It writes the .gitignore only when
// the file is missing, so that a project's own changes to it stay.
func Open(root string) (*Store, error) {
	dir := filepath.Join(root, ".duolog")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".gitignore"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if errors.Is(err, fs.ErrExist) {
		return &Store{Dir: dir}, nil
	}
	if err != nil {
		return nil, err
	}
	_, werr := f.WriteString(GitIgnore)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return nil, werr
	}
	return &Store{Dir: dir}, nil
}

// KeepFor is how long a delegation's folder under tasks/ and an artifact stay
// after the last change to them. The lead reads an answer as soon as the
// delegation returns, so a week leaves room for a resumed session.
const KeepFor = 7 * 24 * time.Hour

// Prune removes what duolog no longer reads: task folders and artifacts that
// nothing changed for KeepFor. The hooks call it when a lead session starts, so
// the folder stays small without anyone running a command. Errors are ignored:
// a file that cannot go now goes at a later start.
func Prune(dir string, now time.Time) {
	for _, sub := range []string{"tasks", "artifacts"} {
		parent := filepath.Join(dir, sub)
		entries, err := os.ReadDir(parent)
		if err != nil {
			continue
		}
		for _, e := range entries {
			path := filepath.Join(parent, e.Name())
			if now.Sub(lastChange(path)) > KeepFor {
				_ = os.RemoveAll(path)
			}
		}
	}
}

// lastChange is the newest modification time of path and, for a folder, of
// the files directly in it: a running worker appends to its log.
func lastChange(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	latest := info.ModTime()
	if !info.IsDir() {
		return latest
	}
	entries, _ := os.ReadDir(path)
	for _, e := range entries {
		if i, err := e.Info(); err == nil && i.ModTime().After(latest) {
			latest = i.ModTime()
		}
	}
	return latest
}
