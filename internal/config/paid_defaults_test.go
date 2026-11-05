package config

import (
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// Workers no longer inherit billing keys by default; an explicit
// env_passthrough entry passes them.
func TestDefaultEnvHasNoBillingKeys(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		names := DefaultEnvPassthrough(goos)
		for _, name := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY"} {
			if slices.Contains(names, name) {
				t.Errorf("%s default passes %s: %v", goos, name, names)
			}
		}
		for _, name := range []string{"PATH", "HOME", "USER", "CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
			if !slices.Contains(names, name) {
				t.Errorf("%s missing %s", goos, name)
			}
		}
		if goos == "windows" {
			for _, name := range []string{"SYSTEMROOT", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP", "PATHEXT", "COMSPEC", "PSMODULEPATH"} {
				if !slices.Contains(names, name) {
					t.Errorf("Windows missing %s", name)
				}
			}
		}
	}
}

// Route names in an explicit list are gated per agent by Task 7; here an
// unset list gets the defaults and an explicit empty list stays empty.
func TestExplicitPassthroughStillReplacesDefaults(t *testing.T) {
	cfg, err := Load(isolatedRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if want := DefaultEnvPassthrough(runtime.GOOS); !slices.Equal(cfg.EnvPassthrough, want) {
		t.Errorf("unset env_passthrough = %v, want defaults %v", cfg.EnvPassthrough, want)
	}

	root := isolatedRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "version: 1\nenv_passthrough: []\n")
	cfg, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EnvPassthrough == nil || len(cfg.EnvPassthrough) != 0 {
		t.Errorf("explicit empty env_passthrough = %#v, want empty", cfg.EnvPassthrough)
	}

	unset := &Config{}
	platformDefaults(unset)
	if !slices.Equal(unset.EnvPassthrough, DefaultEnvPassthrough(runtime.GOOS)) {
		t.Errorf("nil env_passthrough = %v, want defaults", unset.EnvPassthrough)
	}
	empty := &Config{EnvPassthrough: []string{}}
	platformDefaults(empty)
	if empty.EnvPassthrough == nil || len(empty.EnvPassthrough) != 0 {
		t.Errorf("empty env_passthrough = %#v, want empty", empty.EnvPassthrough)
	}
}
