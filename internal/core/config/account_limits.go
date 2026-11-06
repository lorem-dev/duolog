package config

import (
	"sync"

	"go.yaml.in/yaml/v3"
)

// SessionTiers are the most live sessions of one agent at each measured
// subscription tier. Only the user config may set one above its default; Load
// caps the others there. The tiers are separate limits: raising one does not
// raise the next.
type SessionTiers struct {
	Small  int `yaml:"small" jsonschema_description:"Live sessions of an agent at the small tier: unmeasured, paid, workspace or a window like a personal plan of about 20 USD a month; defaults to 3." jsonschema:"minimum=1"`
	Medium int `yaml:"medium" jsonschema_description:"Live sessions of an agent whose measured window is about 5 times the small one; defaults to 5." jsonschema:"minimum=1"`
	Large  int `yaml:"large" jsonschema_description:"Live sessions of an agent whose measured window is about 20 times the small one; defaults to 10." jsonschema:"minimum=1"`
}

// Limit is the session count of tier small, medium or large; any other name
// gets the small count.
func (t SessionTiers) Limit(tier string) int {
	switch tier {
	case "medium":
		return t.Medium
	case "large":
		return t.Large
	}
	return t.Small
}

// Balance says how far duolog trusts quota reads and learned window rates.
type Balance struct {
	MaxAgeSeconds    int     `yaml:"max_age_seconds" jsonschema_description:"Oldest quota read, in seconds, that a verdict or an admission trusts; an older one is read again or counts as paid. Defaults to 120." jsonschema:"minimum=1"`
	MinIntervalShare float64 `yaml:"min_interval_share" jsonschema_description:"Share of a window an interval must move before it measures a rate, above 0 and at most 1; a tier needs 3 such intervals. Defaults to 0.05." jsonschema:"exclusiveMinimum=0,maximum=1"`
	Margin           float64 `yaml:"margin" jsonschema_description:"How far past a tier cut a rate must be before the tier steps up, and how far a new rate may drift before older rates are dropped, 0 or more and below 1; a step down applies at once. Defaults to 0.2." jsonschema:"minimum=0,exclusiveMaximum=1"`
}

// embeddedAccountLimits are the session tiers and balance settings of the
// embedded config.default.yaml; TestEmbeddedDefaultsAreValid keeps it valid.
var embeddedAccountLimits = sync.OnceValue(func() (out struct {
	Limits struct {
		SessionsByTier SessionTiers `yaml:"sessions_by_tier"`
	} `yaml:"limits"`
	Balance Balance `yaml:"balance"`
}) {
	_ = yaml.Unmarshal(defaultConfig, &out)
	return out
})

// DefaultSessionTiers returns the embedded session tiers, the most any layer
// but the user config may set.
func DefaultSessionTiers() SessionTiers {
	return embeddedAccountLimits().Limits.SessionsByTier
}

// DefaultBalance returns the embedded balance settings.
func DefaultBalance() Balance {
	return embeddedAccountLimits().Balance
}
