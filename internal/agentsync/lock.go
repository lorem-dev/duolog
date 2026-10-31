package agentsync

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/store"
)

// Lock records which files duolog generated, from what, and in which direction.
// Files is keyed by the slash-separated path of the generated file, relative to
// the project root. A file that is not in the lock is never overwritten or
// deleted.
type Lock struct {
	Direction string               `json:"direction"`
	Files     map[string]FileEntry `json:"files"`
}

// FileEntry describes one generated file.
type FileEntry struct {
	Source        string `json:"source"`
	SourceHash    string `json:"source_hash"`
	TargetHash    string `json:"target_hash"`
	OriginalName  string `json:"original_name,omitempty"`
	ResolvedModel string `json:"resolved_model,omitempty"`
}

// LockPath returns the lock file of a project.
func LockPath(root string) string {
	stateRoot := project.StateRoot(root)
	if stateRoot != filepath.Clean(root) {
		canonical := filepath.Clean(root)
		if real, err := filepath.EvalSymlinks(root); err == nil {
			canonical = real
		}
		return filepath.Join(stateRoot, ".duolog", "worktrees", hashBytes([]byte(canonical))[:16], "sync.lock.json")
	}
	return filepath.Join(root, ".duolog", "sync.lock.json")
}

// LoadLock reads the lock file. A missing file gives an empty lock.
func LoadLock(root string) (Lock, error) {
	lock := Lock{Files: map[string]FileEntry{}}
	data, err := os.ReadFile(LockPath(root))
	if errors.Is(err, os.ErrNotExist) {
		stateRoot := project.StateRoot(root)
		if stateRoot != filepath.Clean(root) {
			main, err := LoadLock(stateRoot)
			if err != nil {
				return Lock{}, err
			}
			lock.Direction = main.Direction
			// Files committed with this checkout stay owned on the first sync here.
			committed, err := os.ReadFile(filepath.Join(root, ".duolog", "sync.lock.json"))
			if errors.Is(err, os.ErrNotExist) {
				return lock, nil
			}
			if err != nil {
				return Lock{}, err
			}
			var tracked Lock
			if err := json.Unmarshal(committed, &tracked); err != nil {
				return Lock{}, fmt.Errorf("%s: %w", filepath.Join(root, ".duolog", "sync.lock.json"), err)
			}
			if tracked.Files != nil {
				lock.Files = tracked.Files
			}
			if tracked.Direction != "" {
				lock.Direction = tracked.Direction
			}
		}
		return lock, nil
	}
	if err != nil {
		return Lock{}, err
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return Lock{}, fmt.Errorf("%s: %w", LockPath(root), err)
	}
	if lock.Files == nil {
		lock.Files = map[string]FileEntry{}
	}
	return lock, nil
}

// encode writes the lock with sorted keys, so a diff stays stable.
func (l Lock) encode() ([]byte, error) {
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// saveLock writes the lock only when its bytes changed.
func saveLock(root string, l Lock) error {
	data, err := l.encode()
	if err != nil {
		return err
	}
	if old, err := os.ReadFile(LockPath(root)); err == nil && bytes.Equal(old, data) {
		return nil
	}
	return writeAtomic(LockPath(root), data, 0o644)
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// writeAtomic writes through a temporary file in the same directory, then renames.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".duolog-*")
	if err != nil {
		return err
	}
	name := file.Name()
	_, werr := file.Write(data)
	cerr := file.Close()
	if werr == nil && cerr == nil && runtime.GOOS != "windows" {
		cerr = os.Chmod(name, mode)
	}
	if werr != nil || cerr != nil {
		os.Remove(name)
		return errors.Join(werr, cerr)
	}
	if err := store.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
