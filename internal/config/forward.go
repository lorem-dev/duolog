// Package config forwards the names of internal/core/config, the types,
// defaults and lookups, and internal/adapter/config, the loading, to the
// packages that still import it. It holds no logic.
package config

import (
	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	coreconfig "github.com/lorem-dev/duolog/internal/core/config"
)

type (
	AgentSync           = coreconfig.AgentSync
	AutoAnswer          = coreconfig.AutoAnswer
	Balance             = coreconfig.Balance
	Calibration         = coreconfig.Calibration
	CalibrationFilter   = coreconfig.CalibrationFilter
	Capabilities        = coreconfig.Capabilities
	Class               = coreconfig.Class
	ClassRule           = coreconfig.ClassRule
	Coauthors           = coreconfig.Coauthors
	Config              = coreconfig.Config
	ConfigNotice        = coreconfig.ConfigNotice
	EntryStat           = coreconfig.EntryStat
	LadderEntry         = coreconfig.LadderEntry
	LearnedLadder       = coreconfig.LearnedLadder
	LearnedRouting      = coreconfig.LearnedRouting
	Learning            = coreconfig.Learning
	Limits              = coreconfig.Limits
	MissingCapabilities = coreconfig.MissingCapabilities
	Model               = coreconfig.Model
	PaidAgent           = coreconfig.PaidAgent
	PaidClaude          = coreconfig.PaidClaude
	PaidRuns            = coreconfig.PaidRuns
	Policy              = coreconfig.Policy
	Rex                 = coreconfig.Rex
	Role                = coreconfig.Role
	Routing             = coreconfig.Routing
	Rule                = coreconfig.Rule
	SessionTiers        = coreconfig.SessionTiers
	Sessions            = coreconfig.Sessions
	SkillSync           = coreconfig.SkillSync
	Sync                = coreconfig.Sync
	Target              = coreconfig.Target
	Telemetry           = coreconfig.Telemetry
	Wire                = coreconfig.Wire
	WorkerMode          = coreconfig.WorkerMode
	WorkerModes         = coreconfig.WorkerModes
)

const (
	DefaultCap         = coreconfig.DefaultCap
	LearnedFile        = coreconfig.LearnedFile
	LearnedRoutingFile = coreconfig.LearnedRoutingFile
	SchemaURL          = coreconfig.SchemaURL
)

var (
	CheckFeature          = coreconfig.CheckFeature
	DefaultBalance        = coreconfig.DefaultBalance
	DefaultEnvPassthrough = coreconfig.DefaultEnvPassthrough
	DefaultModels         = coreconfig.DefaultModels
	DefaultRouting        = coreconfig.DefaultRouting
	DefaultSessionTiers   = coreconfig.DefaultSessionTiers
	DefaultYAML           = coreconfig.DefaultYAML
	Families              = coreconfig.Families
	Family                = coreconfig.Family
	Features              = coreconfig.Features
	LadderKey             = coreconfig.LadderKey
	MCPToolRules          = coreconfig.MCPToolRules
	ParseEntryKey         = coreconfig.ParseEntryKey
	RouteFamilies         = coreconfig.RouteFamilies
	SafeAutoAnswer        = coreconfig.SafeAutoAnswer

	LearnedRules       = configfile.LearnedRules
	Load               = configfile.Load
	ReadLearnedRouting = configfile.ReadLearnedRouting
	RefuseRoot         = configfile.RefuseRoot
	RefuseRootEnv      = configfile.RefuseRootEnv
)
