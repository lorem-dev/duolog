package delegate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerEnvPinsExecutableDirectory(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, parent := range []string{"", t.TempDir()} {
		t.Setenv("PATH", parent)
		for _, names := range [][]string{nil, {"PATH", "Path"}} {
			pairs := workerEnv(names, "lead", "docs")
			want := "PATH=" + filepath.Dir(exe)
			if parent != "" {
				want += string(os.PathListSeparator) + parent
			}
			count := 0
			for _, pair := range pairs {
				if strings.EqualFold(strings.SplitN(pair, "=", 2)[0], "PATH") {
					count++
					if pair != want {
						t.Fatal(pair, want)
					}
				}
			}
			if count != 1 {
				t.Fatal("expected one pinned PATH", pairs)
			}
		}
	}
}
