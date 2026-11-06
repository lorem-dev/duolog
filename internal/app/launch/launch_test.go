package launch

import (
	"os"
	"testing"
)

func TestIsWorker(t *testing.T) {
	cases := []struct {
		name  string
		set   bool
		value string
		want  bool
	}{
		{"unset", false, "", false},
		{"empty", true, "", false},
		{"zero", true, "0", false},
		{"one", true, "1", true},
		{"two", true, "2", true},
		{"not a number", true, "x", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// t.Setenv restores the old value when the subtest ends, so the
			// unset case may remove the variable after it.
			t.Setenv("DUOLOG_DEPTH", c.value)
			if !c.set {
				if err := os.Unsetenv("DUOLOG_DEPTH"); err != nil {
					t.Fatal(err)
				}
			}
			if got := IsWorker(); got != c.want {
				t.Errorf("IsWorker() with DUOLOG_DEPTH %q (set %v) = %v, want %v", c.value, c.set, got, c.want)
			}
		})
	}
}
