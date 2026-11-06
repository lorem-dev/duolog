package kit

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/agent"
)

func TestHome(t *testing.T) {
	homeVar := "HOME"
	if runtime.GOOS == "windows" {
		homeVar = "USERPROFILE"
	}
	home := filepath.Join(t.TempDir(), "h")
	custom := filepath.Join(t.TempDir(), "c")
	for _, c := range []struct {
		name    string
		environ []string
		want    string
		noHome  bool
	}{
		{"default under home", []string{homeVar + "=" + home}, filepath.Join(home, ".codex"), false},
		{"variable wins", []string{homeVar + "=" + home, "CODEX_HOME=" + custom}, custom, false},
		{"variable alone", []string{"CODEX_HOME=" + custom}, custom, false},
		{"last entry wins", []string{homeVar + "=" + home, "CODEX_HOME=" + home, "CODEX_HOME=" + custom}, custom, false},
		{"no home", []string{"PATH=" + home}, "", true},
		{"empty home", []string{homeVar + "="}, "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := Home(c.environ, "CODEX_HOME", ".codex")
			if c.noHome {
				if !errors.Is(err, agent.ErrNoHome) {
					t.Fatalf("Home = %q, %v; want ErrNoHome", got, err)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("Home = %q, %v; want %q", got, err, c.want)
			}
		})
	}
}
