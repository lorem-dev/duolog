package config

import (
	_ "embed"
	"runtime"
)

//go:embed config.default.yaml
var defaultConfig []byte

// DefaultEnvPassthrough includes native login and CLI configuration variables.
// API keys are left out: a worker that inherits one is billed per token.
func DefaultEnvPassthrough(goos string) []string {
	names := []string{"PATH", "HOME", "USER", "CODEX_HOME", "CLAUDE_CONFIG_DIR"}
	if goos == "windows" {
		names = append(names, "SYSTEMROOT", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP", "PATHEXT", "COMSPEC", "PSMODULEPATH")
	}
	return names
}
func platformDefaults(cfg *Config) {
	if cfg.EnvPassthrough == nil {
		cfg.EnvPassthrough = DefaultEnvPassthrough(runtime.GOOS)
	}
}
