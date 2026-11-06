// Package config forwards the names of internal/core/config, the types,
// defaults and lookups, and internal/adapter/config, the loading, that the
// security boundary (internal/gate, internal/paidruns, cmd/duolog's gate.go,
// leadguard.go and remember.go, and their tests) still imports. It holds no
// logic.
package config

import (
	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	coreconfig "github.com/lorem-dev/duolog/internal/core/config"
)

type (
	Balance      = coreconfig.Balance
	Config       = coreconfig.Config
	ConfigNotice = coreconfig.ConfigNotice
	Model        = coreconfig.Model
	PaidAgent    = coreconfig.PaidAgent
	PaidRuns     = coreconfig.PaidRuns
	Policy       = coreconfig.Policy
	Routing      = coreconfig.Routing
	Rule         = coreconfig.Rule
	SessionTiers = coreconfig.SessionTiers
	Target       = coreconfig.Target
	Wire         = coreconfig.Wire
)

const LearnedFile = coreconfig.LearnedFile

var (
	DefaultBalance        = coreconfig.DefaultBalance
	DefaultEnvPassthrough = coreconfig.DefaultEnvPassthrough
	DefaultModels         = coreconfig.DefaultModels
	DefaultSessionTiers   = coreconfig.DefaultSessionTiers

	LearnedRules = configfile.LearnedRules
	Load         = configfile.Load
)
