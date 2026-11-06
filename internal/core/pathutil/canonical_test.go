package pathutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The macOS data volume's firmlinks: /System/Volumes/Data/Users is /Users.
func TestCanonicalMapsFirmlinks(t *testing.T) {
	for in, want := range map[string]string{
		"/System/Volumes/Data/Users/study/.ssh": "/Users/study/.ssh",
		"/system/volumes/DATA/Users/study":      "/Users/study",
		"/System/Volumes/Data":                  "/",
		"/System/Volumes/Data/":                 "/",
		"/System/Volumes/DataX/Users":           "/System/Volumes/DataX/Users",
		"/Users/study":                          "/Users/study",
	} {
		if got := firmlinked("darwin", in); got != want {
			t.Errorf("firmlinked(%s) = %s, want %s", in, got, want)
		}
	}
	if got := firmlinked("linux", "/System/Volumes/Data/Users"); got != "/System/Volumes/Data/Users" {
		t.Errorf("linux maps %s", got)
	}
	if runtime.GOOS != "darwin" {
		return
	}
	home := Resolved(t.TempDir())
	if err := os.Mkdir(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, alias := range []string{filepath.Join("/System/Volumes/Data", home, ".ssh"), filepath.Join("/System/Volumes/Data", home)} {
		if !Overlap(filepath.Join(home, ".ssh"), alias) {
			t.Errorf("%s does not overlap %s", alias, filepath.Join(home, ".ssh"))
		}
	}
}

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
