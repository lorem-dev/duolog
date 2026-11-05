package workerenv

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestGateBinary(t *testing.T) {
	dir := t.TempDir()
	name := "duolog"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if _, err := GateBinary("PATH=" + dir); err == nil {
		t.Fatal("found duolog in an empty folder")
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := GateBinary("PATH=" + t.TempDir() + string(os.PathListSeparator) + dir)
	if err != nil || got != filepath.Join(dir, name) {
		t.Fatal(got, err)
	}
	if runtime.GOOS != "windows" {
		other := t.TempDir()
		if err := os.WriteFile(filepath.Join(other, "duolog"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := GateBinary("PATH=" + other); err == nil {
			t.Fatal("accepted a file that is not executable")
		}
	}
}
