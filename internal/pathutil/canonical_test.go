package pathutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOverlapIsCanonical(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "Secret")
	if err := os.MkdirAll(filepath.Join(secret, "keys"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "cache")
	if err := os.Symlink(secret, link); err != nil {
		t.Skip("no symlinks:", err)
	}
	for _, root := range []string{secret, filepath.Join(secret, "new", "dir"), dir, link, filepath.Join(link, "keys"), filepath.Join(link, "missing")} {
		if !Overlap(secret, root) {
			t.Errorf("%s does not overlap %s", root, secret)
		}
	}
	if Overlap(secret, filepath.Join(dir, "Other")) || Overlap(secret, t.TempDir()) {
		t.Error("unrelated folders overlap")
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		if !Overlap(secret, strings.ToUpper(secret)) || !Overlap(secret, filepath.Join(dir, "secret", "x")) {
			t.Error("case variants do not overlap")
		}
	}
}
