package workerenv

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
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

// The reserved prefixes hold in any case: on Windows environment names ignore
// case, so a passthrough of rex_session would read the parent's REX_SESSION.
func TestReservedIgnoresCase(t *testing.T) {
	for _, name := range []string{"DUOLOG_DEPTH", "duolog_depth", "Duolog_Peer", "REX_SESSION", "rex_session", "Rex_Session", "rEx_X"} {
		if !Reserved(name) {
			t.Errorf("%s is not reserved", name)
		}
	}
	for _, name := range []string{"HOME", "DUOLOG", "REX", "MY_DUOLOG_X", "rexx_session", "PATH"} {
		if Reserved(name) {
			t.Errorf("%s is reserved", name)
		}
	}
}

func TestPassthroughDropsReservedNamesInAnyCase(t *testing.T) {
	t.Setenv("rex_session", "lead-rex-session")
	t.Setenv("Duolog_Depth", "0")
	t.Setenv("DUOLOG_TEST_KEEP", "x")
	t.Setenv("WORKERENV_TEST_KEEP", "kept")
	got := Passthrough([]string{"rex_session", "Duolog_Depth", "DUOLOG_TEST_KEEP", "WORKERENV_TEST_KEEP"})
	if want := []string{"WORKERENV_TEST_KEEP=kept"}; !slices.Equal(got, want) {
		t.Fatalf("Passthrough = %v, want %v", got, want)
	}
}
