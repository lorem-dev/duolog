package config

import (
	"runtime"

	"github.com/lorem-dev/duolog/internal/core/config"
)

func platformDefaults(cfg *config.Config) {
	if cfg.EnvPassthrough == nil {
		cfg.EnvPassthrough = config.DefaultEnvPassthrough(runtime.GOOS)
	}
}
