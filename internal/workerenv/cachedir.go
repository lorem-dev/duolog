package workerenv

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// cacheWarnings is where a refused cache folder is reported.
var cacheWarnings io.Writer = os.Stderr

// goCacheHash is a short hash of the lead root, which names the cache folder
// of a project: the worktrees of one project share it, two projects do not.
func goCacheHash(lead string) string {
	sum := sha256.Sum256([]byte(lead))
	return hex.EncodeToString(sum[:])[:12]
}

// prepareCacheDirs creates the cache folders with mode 0700 and checks that
// base and each folder is a real folder (not a link) owned by the current
// user and not writable by the group or the world.
func prepareCacheDirs(base string, subs ...string) error {
	dirs := []string{filepath.Dir(base), base}
	for _, sub := range subs {
		dirs = append(dirs, filepath.Join(base, sub))
	}
	for _, dir := range dirs {
		if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
			return err
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is not a folder", dir)
		}
		if !ownedByUser(info) {
			return fmt.Errorf("%s is not owned by the current user", dir)
		}
		if info.Mode().Perm()&fs.FileMode(0o022) != 0 && !windowsModes {
			return fmt.Errorf("%s is writable by the group or the world", dir)
		}
	}
	return nil
}
