package delegate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/launch/launchtest"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
)

func TestWorkerEnvPinsExecutableDirectory(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, parent := range []string{"", t.TempDir()} {
		t.Setenv("PATH", parent)
		for _, names := range [][]string{nil, {"PATH", "Path"}} {
			p, err := workerEnv(launchtest.Build, corelaunch.ProcessInput{Agent: "codex", Variant: corelaunch.Free,
				Config: &config.Config{EnvPassthrough: names}, Environ: os.Environ(), LeadID: "lead", Role: "docs"})
			if err != nil {
				t.Fatal(err)
			}
			want := "PATH=" + filepath.Dir(exe)
			if parent != "" {
				want += string(os.PathListSeparator) + parent
			}
			count := 0
			for _, pair := range p.Env {
				if strings.EqualFold(strings.SplitN(pair, "=", 2)[0], "PATH") {
					count++
					if pair != want {
						t.Fatal(pair, want)
					}
				}
			}
			if count != 1 || p.Shape != corelaunch.Worker {
				t.Fatal("expected one pinned PATH in a worker", p.Env)
			}
		}
	}
}
