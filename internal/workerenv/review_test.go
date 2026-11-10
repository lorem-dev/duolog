package workerenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCacheRejectsUnsafeParent(t *testing.T) {
	// Mutation killed: checking only the hash folder permits parent replacement by another user.
	if windowsModes {
		t.Skip("POSIX permissions")
	}
	parent := filepath.Join(t.TempDir(), "duolog-go-cache")
	if err := os.Mkdir(parent, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0777); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(parent, "project")
	if err := prepareCacheDirs(base, "go-build"); err == nil {
		t.Fatal("accepted writable parent")
	}
	if _, err := os.Stat(base); !os.IsNotExist(err) {
		t.Fatal("created cache below unsafe parent")
	}
}
