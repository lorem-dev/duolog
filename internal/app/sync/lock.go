package agentsync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

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
		return filepath.Join(stateRoot, ".duolog", "worktrees", worktreeKey(root), "sync.lock.json")
	}
	return filepath.Join(root, ".duolog", "sync.lock.json")
}

// worktreeKey names the folder that holds a linked worktree's lock.
func worktreeKey(root string) string {
	canonical := filepath.Clean(root)
	if real, err := filepath.EvalSymlinks(root); err == nil {
		canonical = real
	}
	return hashBytes([]byte(canonical))[:16]
}

// pruneWorktreeLocks removes the lock folders of linked worktrees that Git no
// longer lists, so that removed worktrees do not pile up under .duolog/. It
// keeps every folder when Git cannot list the worktrees, a folder of any
// spelling of a listed path, and a folder that holds anything but the lock.
func pruneWorktreeLocks(root string) {
	stateRoot := project.StateRoot(root)
	dir := filepath.Join(stateRoot, ".duolog", "worktrees")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return
	}
	// A sync in a new worktree may be writing its first lock: try once, and
	// leave the cleanup to a later session when the lock is held.
	unlock, locked, err := store.TryLockFile(filepath.Join(stateRoot, ".duolog", "sync.lock"))
	if err != nil || !locked {
		return
	}
	defer func() { _ = unlock() }()
	out, err := project.Git(context.Background(), nil, "-C", stateRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return
	}
	live := map[string]bool{}
	var path string
	for _, line := range strings.Split(strings.ReplaceAll(string(out)+"\n", "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "prunable"):
			// Git still lists a worktree whose folder is gone until it is pruned.
			path = ""
		case line == "" && path != "":
			for _, p := range spellings(path) {
				live[worktreeKey(p)] = true
				live[hashBytes([]byte(p))[:16]] = true
			}
			path = ""
		}
	}
	for _, e := range entries {
		if !e.IsDir() || live[e.Name()] {
			continue
		}
		folder := filepath.Join(dir, e.Name())
		if err := os.Remove(filepath.Join(folder, "sync.lock.json")); err == nil || errors.Is(err, os.ErrNotExist) {
			_ = os.Remove(folder) // fails, and keeps the folder, when it holds anything else
		}
	}
}

// spellings lists the forms of a path that LockPath may have hashed: Git
// prints forward slashes, and Windows paths may differ in drive letter case.
func spellings(path string) []string {
	p := filepath.Clean(filepath.FromSlash(path))
	out := []string{p}
	if runtime.GOOS == "windows" && len(p) > 1 && p[1] == ':' {
		out = append(out, strings.ToUpper(p[:1])+p[1:], strings.ToLower(p[:1])+p[1:])
	}
	return out
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
