package config

import (
	"slices"
	"testing"
)

func TestNativeAndLoginPassthroughDefaults(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		names := DefaultEnvPassthrough(goos)
		for _, name := range []string{"PATH", "HOME", "USER", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
			if !slices.Contains(names, name) {
				t.Errorf("%s missing %s", goos, name)
			}
		}
		if goos == "windows" {
			for _, name := range []string{"SYSTEMROOT", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TEMP", "PATHEXT", "COMSPEC", "PSMODULEPATH"} {
				if !slices.Contains(names, name) {
					t.Errorf("Windows missing %s", name)
				}
			}
		}
	}
}
