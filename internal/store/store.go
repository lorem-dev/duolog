package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// GitIgnore keeps shared settings in Git and everything a session writes out of it.
const GitIgnore = `# duolog's local state stays out of Git: the bus, sessions, tasks and caches.
*
# Share these with the project.
!.gitignore
!config.yaml
!sync.lock.json
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
