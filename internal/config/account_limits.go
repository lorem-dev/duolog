package config

import (
	"fmt"
	"math"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
)

// SessionTiers are the most live sessions of one agent at each measured
// subscription tier. Load caps each at its default.
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
// may set.
func DefaultSessionTiers() SessionTiers {
	return embeddedAccountLimits().Limits.SessionsByTier
}

// DefaultBalance returns the embedded balance settings.
func DefaultBalance() Balance {
	return embeddedAccountLimits().Balance
}

// accountLimit is one numeric account limit: its full key, whether it is a
// fraction, its bound and which direction a shared project may move it.
type accountLimit struct {
	key      string
	fraction bool
	valid    func(float64) bool
	want     string
	// higherIsLooser is true when a larger value allows more.
	higherIsLooser bool
	get            func(*Config) float64
}

// Bounds are negated-safe: NaN compares false, so it never passes.
var accountLimits = []accountLimit{
	{"limits.max_total_sessions", false, atLeast(1), "must be above 0", true,
		func(c *Config) float64 { return float64(c.Limits.MaxTotalSessions) }},
	{"limits.sessions_by_tier.small", false, atLeast(1), "must be above 0", true,
		func(c *Config) float64 { return float64(c.Limits.SessionsByTier.Small) }},
	{"limits.sessions_by_tier.medium", false, atLeast(1), "must be above 0", true,
		func(c *Config) float64 { return float64(c.Limits.SessionsByTier.Medium) }},
	{"limits.sessions_by_tier.large", false, atLeast(1), "must be above 0", true,
		func(c *Config) float64 { return float64(c.Limits.SessionsByTier.Large) }},
	{"limits.session_wait_seconds", false, atLeast(0), "must be 0 or more", true,
		func(c *Config) float64 { return float64(c.Limits.SessionWaitSeconds) }},
	{"balance.max_age_seconds", false, atLeast(1), "must be above 0", true,
		func(c *Config) float64 { return float64(c.Balance.MaxAgeSeconds) }},
	{"balance.min_interval_share", true, func(v float64) bool { return v > 0 && v <= 1 }, "must be above 0 and at most 1", false,
		func(c *Config) float64 { return c.Balance.MinIntervalShare }},
	{"balance.margin", true, func(v float64) bool { return v >= 0 && v < 1 }, "must be 0 or more and below 1", false,
		func(c *Config) float64 { return c.Balance.Margin }},
	// A larger routing margin balances later, so a shared project may only lower it.
	{"routing.margin", true, func(v float64) bool { return v >= 0 && v < 1 }, "must be 0 or more and below 1", true,
		func(c *Config) float64 { return c.Routing.Margin }},
}

func atLeast(n float64) func(float64) bool {
	return func(v float64) bool { return v >= n }
}

// checkAccountLimitNodes checks the account limits one resolved layer sets,
// so that a wrong type or value is named by its full key: a count must be a
// whole number in its bound and a fraction a finite one.
func checkAccountLimitNodes(root *yaml.Node) error {
	for _, l := range accountLimits {
		node, err := limitNode(root, l.key)
		if err != nil {
			return err
		}
		if node == nil {
			continue
		}
		// Decode as a number first: the YAML decoder truncates 2.5 into an
		// int without an error.
		var v float64
		if err := node.Decode(&v); err != nil {
			return fmt.Errorf("%s: %w", l.key, err)
		}
		if !l.fraction && !(v == math.Trunc(v) && math.Abs(v) < 1<<53) {
			return fmt.Errorf("%s: must be a whole number", l.key)
		}
		if !l.valid(v) || math.IsInf(v, 0) {
			return fmt.Errorf("%s: %s", l.key, l.want)
		}
	}
	return nil
}

// limitNode finds the node of a dotted key in a resolved mapping, nil when a
// layer leaves it out. A section that is present but not a mapping is an
// error named by its key.
func limitNode(root *yaml.Node, key string) (*yaml.Node, error) {
	node := root
	parts := strings.Split(key, ".")
	for i, part := range parts {
		if node.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("%s: must be a mapping", strings.Join(parts[:i], "."))
		}
		if node = mappingValue(node, part); node == nil {
			return nil, nil
		}
	}
	return node, nil
}

// validateAccountLimits checks the merged account limits.
func validateAccountLimits(cfg *Config) error {
	for _, l := range accountLimits {
		if !l.valid(l.get(cfg)) {
			return fmt.Errorf("%s: %s", l.key, l.want)
		}
	}
	return nil
}

// capSessionTiers lowers each session tier above its default to the default
// and names it. A larger window may hold more parallel work, but never more
// than the default tier allows.
func capSessionTiers(cfg *Config) []ConfigNotice {
	defaults := DefaultSessionTiers()
	var notices []ConfigNotice
	for _, t := range []struct {
		name  string
		value *int
		max   int
	}{
		{"small", &cfg.Limits.SessionsByTier.Small, defaults.Small},
		{"medium", &cfg.Limits.SessionsByTier.Medium, defaults.Medium},
		{"large", &cfg.Limits.SessionsByTier.Large, defaults.Large},
	} {
		if *t.value > t.max {
			notices = append(notices, ConfigNotice{Key: "limits.sessions_by_tier." + t.name, Reason: fmt.Sprintf(
				"%d is above the default %d, the most a tier may hold; %d applies", *t.value, t.max, t.max)})
			*t.value = t.max
		}
	}
	return notices
}

// restrictProjectSessionLimits removes from the project layer each account
// limit that would allow more than the layers below, before the layer is
// merged: the total, the tiers and the wait may only go down, the quota age
// down, and the interval share and the margin up. Only keys the layer sets
// are compared, so an omitted key keeps the value below it. Both nodes are
// resolved mappings, so aliases and merge keys cannot hide a wider value.
// Other limits, such as max_live_sessions, are left as they are.
func restrictProjectSessionLimits(base, layer *yaml.Node) ([]ConfigNotice, error) {
	var notices []ConfigNotice
	for _, l := range accountLimits {
		cut := strings.LastIndex(l.key, ".")
		parent, err := limitNode(layer, l.key[:cut])
		if err != nil {
			return nil, err
		}
		name := l.key[cut+1:]
		value := mappingValue(parent, name)
		if value == nil {
			continue
		}
		baseValue, err := limitNode(base, l.key)
		if err != nil {
			return nil, err
		}
		var number, baseNumber float64
		if err := decodeOptional(value, &number); err != nil {
			return nil, err
		}
		if err := decodeOptional(baseValue, &baseNumber); err != nil {
			return nil, err
		}
		// A missing base counts as 0, as for paid runs.
		looser := number > baseNumber
		if !l.higherIsLooser {
			looser = number < baseNumber
		}
		if !looser {
			continue
		}
		parent.Content, _ = keepEntries(parent.Content, func(key string, _ *yaml.Node) (bool, error) {
			return key != name, nil
		})
		notices = append(notices, ConfigNotice{Key: l.key, Reason: fmt.Sprintf(
			"%s in %s is ignored: a shared config may only make session and balance limits stricter; %s applies",
			formatNumber(number), projectFile, formatNumber(baseNumber))})
	}
	return notices, nil
}
