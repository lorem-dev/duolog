package config

import (
	_ "embed"
	"runtime"
)

//go:embed config.default.yaml
var defaultConfig []byte

// DefaultEnvPassthrough includes native login and CLI configuration variables.
func DefaultEnvPassthrough(goos string) []string {
	names := []string{"PATH", "HOME", "USER", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "CODEX_HOME", "CLAUDE_CONFIG_DIR"}
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
