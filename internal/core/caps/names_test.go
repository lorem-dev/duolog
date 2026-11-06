package caps

import "testing"

func TestClaudeName(t *testing.T) {
	for in, want := range map[string]string{
		"my.server": "my_server",
		"a-b_c9":    "a-b_c9",
		"x y/z":     "x_y_z",
	} {
		if got := ClaudeName(in); got != want {
			t.Errorf("ClaudeName(%q) = %q, want %q", in, got, want)
		}
	}
}
