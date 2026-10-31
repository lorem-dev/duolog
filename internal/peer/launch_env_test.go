package peer

import (
	"github.com/lorem-dev/duolog/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentEnvPinsExecutableDirectory(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, parent := range []string{"", t.TempDir()} {
		t.Setenv("PATH", parent)
		for _, agent := range []string{"claude", "codex"} {
			pairs := agentEnv(&config.Config{EnvPassthrough: []string{"PATH", "Path"}}, agent, "lead", "docs")
			count := 0
			want := "PATH=" + filepath.Dir(exe)
			if parent != "" {
				want += string(os.PathListSeparator) + parent
			}
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
