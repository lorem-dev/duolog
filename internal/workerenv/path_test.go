package workerenv

import (
	"slices"
	"testing"
)

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
