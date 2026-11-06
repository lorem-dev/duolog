// Package config forwards the names of internal/core/config, the types,
// defaults and lookups, and internal/adapter/config, the loading, that the
// security boundary (internal/gate, cmd/duolog's gate.go, leadguard.go and
// remember.go, and their tests) still imports. It holds no logic.
package config

import (
	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	coreconfig "github.com/lorem-dev/duolog/internal/core/config"
)

type (
	Config = coreconfig.Config
	Policy = coreconfig.Policy
	Rule   = coreconfig.Rule
	Target = coreconfig.Target
	Wire   = coreconfig.Wire
)

const LearnedFile = coreconfig.LearnedFile

var (
	LearnedRules = configfile.LearnedRules
	Load         = configfile.Load
)
