package config

import (
	_ "embed"
	"fmt"
	"runtime"

	"go.yaml.in/yaml/v3"
)

//go:embed config.default.yaml
var defaultConfig []byte

// DefaultModels returns the models of the embedded config.default.yaml by
// alias, without any user or project layer.
func DefaultModels() (map[string]Model, error) {
	var defaults struct {
		Models map[string]Model `yaml:"models"`
	}
	if err := yaml.Unmarshal(defaultConfig, &defaults); err != nil {
		return nil, fmt.Errorf("embedded config.default.yaml: %w", err)
	}
	return defaults.Models, nil
}

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
