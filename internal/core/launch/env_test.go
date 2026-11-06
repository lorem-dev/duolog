package launch

import (
	"runtime"
	"testing"
)

func TestGetenv(t *testing.T) {
	environ := []string{"PATH=/a", "OTHER=x", "PATH=/b", "EMPTY="}
	for name, want := range map[string]string{"PATH": "/b", "OTHER": "x", "EMPTY": "", "MISSING": ""} {
		if got := Getenv(environ, name); got != want {
			t.Errorf("Getenv(%q) = %q, want %q", name, got, want)
		}
	}
	// Names fold case on Windows only.
	want := ""
	if runtime.GOOS == "windows" {
		want = "x"
	}
	if got := Getenv([]string{"other=x"}, "OTHER"); got != want {
		t.Errorf("Getenv with another case = %q, want %q", got, want)
	}
}
