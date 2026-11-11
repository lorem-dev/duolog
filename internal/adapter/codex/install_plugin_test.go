package codex

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInstalledReadsSelectedCodexHome checks plugin registration, disabled
// entries and malformed or absent config without ever starting the CLI.
func TestInstalledReadsSelectedCodexHome(t *testing.T) {
	for _, tc := range []struct {
		config string
		want   bool
	}{
		{"", false},
		{"not toml [", false},
		{"[plugins.\"other@duolog\"]\nenabled = true\n", false},
		{"[plugins.\"duolog@duolog\"]\nenabled = true\n", true},
		{"[plugins.\"duolog@duolog\"]\nenabled = false\n", true},
	} {
		home, selected := t.TempDir(), t.TempDir()
		path := filepath.Join(selected, "config.toml")
		if tc.config != "" {
			if err := os.WriteFile(path, []byte(tc.config), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		env := []string{"CODEX_HOME=" + selected}
		if got := (Installer{}).Installed("", home, env); got != tc.want {
			t.Errorf("config %q: installed %v, want %v", tc.config, got, tc.want)
		}
		if (Installer{}).Installed("", home, nil) {
			t.Fatal("read a config outside the selected home")
		}
	}
}
