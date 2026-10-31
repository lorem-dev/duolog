package store

import (
	"bytes"
	"os"
	"path/filepath"
)

type Store struct {
	Dir string
}

// Open creates the project state directory and keeps only shared settings in Git.
func Open(root string) (*Store, error) {
	dir := filepath.Join(root, ".duolog")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, ".gitignore")
	want := []byte("*\n!config.yaml\n!sync.lock.json\n!.gitignore\n")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if !bytes.Equal(data, want) {
		if err := os.WriteFile(path, want, 0644); err != nil {
			return nil, err
		}
	}
	return &Store{Dir: dir}, nil
}
