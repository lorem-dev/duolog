package config

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/lorem-dev/duolog/internal/core/config"
)

// accountLimit is one numeric account limit: its full key, whether it is a
// fraction, its bound and which direction a shared project may move it.
type accountLimit struct {
	key      string
	fraction bool
	valid    func(float64) bool
	want     string
	// higherIsLooser is true when a larger value allows more.
	higherIsLooser bool
	get            func(*config.Config) float64
}

// Bounds are negated-safe: NaN compares false, so it never passes.
var accountLimits = []accountLimit{
	{"limits.max_total_sessions", false, atLeast(1), "must be above 0", true,
		func(c *config.Config) float64 { return float64(c.Limits.MaxTotalSessions) }},
	{"limits.sessions_by_tier.small", false, atLeast(1), "must be above 0", true,
		func(c *config.Config) float64 { return float64(c.Limits.SessionsByTier.Small) }},
	{"limits.sessions_by_tier.medium", false, atLeast(1), "must be above 0", true,
		func(c *config.Config) float64 { return float64(c.Limits.SessionsByTier.Medium) }},
	{"limits.sessions_by_tier.large", false, atLeast(1), "must be above 0", true,
		func(c *config.Config) float64 { return float64(c.Limits.SessionsByTier.Large) }},
	{"limits.session_wait_seconds", false, atLeast(0), "must be 0 or more", true,
		func(c *config.Config) float64 { return float64(c.Limits.SessionWaitSeconds) }},
	{"balance.max_age_seconds", false, atLeast(1), "must be above 0", true,
		func(c *config.Config) float64 { return float64(c.Balance.MaxAgeSeconds) }},
	{"balance.min_interval_share", true, func(v float64) bool { return v > 0 && v <= 1 }, "must be above 0 and at most 1", false,
		func(c *config.Config) float64 { return c.Balance.MinIntervalShare }},
	{"balance.margin", true, func(v float64) bool { return v >= 0 && v < 1 }, "must be 0 or more and below 1", false,
		func(c *config.Config) float64 { return c.Balance.Margin }},
	// A larger routing margin balances later, so a shared project may only lower it.
	{"routing.margin", true, func(v float64) bool { return v >= 0 && v < 1 }, "must be 0 or more and below 1", true,
		func(c *config.Config) float64 { return c.Routing.Margin }},
}

// personLimits are the numeric limits that only the person's own layers may
// set: a shared layer's value is removed whole by restrictProjectPersonLimits,
// so they are checked like the account limits but never compared with a base.
var personLimits = []accountLimit{
	// A project must not keep work out of the workers.
	{"routing.small_task_files", false, atLeast(0), "must be 0 or more", false,
		func(c *config.Config) float64 { return float64(c.Routing.SmallTaskFiles) }},
	// A project must not make the lead nag, nor silence the reminder.
	{"lead.fresh_session_hint", false, atLeast(0), "must be 0 or more", false,
		func(c *config.Config) float64 { return float64(c.Lead.FreshSessionHint) }},
}

// personFlags are the switches that only the person's own layers may set,
// removed from a shared layer as the person limits are. They need no bound.
var personFlags = []string{
	// A project must not silence the alerts that call each person back.
	"lead.notify",
}

// personKeys are the keys of the person limits and the person flags.
func personKeys() []string {
	keys := slices.Clone(personFlags)
	for _, l := range personLimits {
		keys = append(keys, l.key)
	}
	return keys
}

// checkedLimits are the limits that are checked for a bound.
func checkedLimits() []accountLimit {
	return append(slices.Clone(accountLimits), personLimits...)
}

// restrictProjectPersonLimits removes each person-only limit and flag from a
// shared layer and names it. The nodes are resolved mappings.
func restrictProjectPersonLimits(_, layer *yaml.Node, projectFile string) ([]config.ConfigNotice, error) {
	var notices []config.ConfigNotice
	for _, key := range personKeys() {
		cut := strings.LastIndex(key, ".")
		parent, err := limitNode(layer, key[:cut])
		if err != nil {
			return nil, err
		}
		name := key[cut+1:]
		value := mappingValue(parent, name)
		if value == nil {
			continue
		}
		parent.Content, _ = keepEntries(parent.Content, func(key string, _ *yaml.Node) (bool, error) {
			return key != name, nil
		})
		notices = append(notices, config.ConfigNotice{Key: key, Reason: fmt.Sprintf(
			"%s in %s is ignored: only the user config or a local config of your own may set this", value.Value, projectFile)})
	}
	return notices, nil
}

func atLeast(n float64) func(float64) bool {
	return func(v float64) bool { return v >= n }
}

// checkAccountLimitNodes checks the account limits one resolved layer sets,
// so that a wrong type or value is named by its full key: a count must be a
// whole number in its bound and a fraction a finite one.
func checkAccountLimitNodes(root *yaml.Node) error {
	for _, l := range checkedLimits() {
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

// validateAccountLimits checks the merged account limits, and with them the
// compact window of the workers, which is checked at the same point.
func validateAccountLimits(cfg *config.Config) error {
	for _, l := range checkedLimits() {
		if !l.valid(l.get(cfg)) {
			return fmt.Errorf("%s: %s", l.key, l.want)
		}
	}
	return validateCompact(cfg.Workers)
}

// capLayerSessionLimits lowers to its default each session count that a layer
// other than the person's user config sets above it, and names it. Only the
// user config may raise the total or a tier: a shared file, or a local one
// that Git ignores but that may sit in any checkout or unpacked archive, does
// not prove that the person wrote it. Raising one tier does not raise the
// next. The node is a resolved mapping; file names the layer in notices.
func capLayerSessionLimits(layer *yaml.Node, file string) ([]config.ConfigNotice, error) {
	tiers := config.DefaultSessionTiers()
	var total struct {
		Limits struct {
			MaxTotalSessions int `yaml:"max_total_sessions"`
		} `yaml:"limits"`
	}
	if err := yaml.Unmarshal(config.DefaultYAML(), &total); err != nil {
		return nil, err
	}
	var notices []config.ConfigNotice
	for _, l := range []struct {
		key string
		max int
	}{
		{"limits.max_total_sessions", total.Limits.MaxTotalSessions},
		{"limits.sessions_by_tier.small", tiers.Small},
		{"limits.sessions_by_tier.medium", tiers.Medium},
		{"limits.sessions_by_tier.large", tiers.Large},
	} {
		value, err := limitNode(layer, l.key)
		if err != nil {
			return nil, err
		}
		if value == nil {
			continue
		}
		var number float64
		if err := decodeOptional(value, &number); err != nil {
			return nil, err
		}
		if number <= float64(l.max) {
			continue
		}
		value.Kind, value.Tag, value.Value, value.Style = yaml.ScalarNode, "!!int", fmt.Sprint(l.max), 0
		value.Content, value.Alias = nil, nil
		notices = append(notices, config.ConfigNotice{Key: l.key, Reason: fmt.Sprintf(
			"%s in %s is ignored: only the user config may raise this above its default; %s applies",
			formatNumber(number), file, formatNumber(float64(l.max)))})
	}
	return notices, nil
}

// restrictProjectSessionLimits removes from the project layer each account
// limit that would allow more than the layers below, before the layer is
// merged: the total, the tiers and the wait may only go down, the quota age
// down, and the interval share and the margin up. Only keys the layer sets
// are compared, so an omitted key keeps the value below it. Both nodes are
// resolved mappings, so aliases and merge keys cannot hide a wider value.
// Other limits, such as max_live_sessions, are left as they are.
func restrictProjectSessionLimits(base, layer *yaml.Node, projectFile string) ([]config.ConfigNotice, error) {
	var notices []config.ConfigNotice
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
		notices = append(notices, config.ConfigNotice{Key: l.key, Reason: fmt.Sprintf(
			"%s in %s is ignored: a shared config may only make session and balance limits stricter; %s applies",
			formatNumber(number), projectFile, formatNumber(baseNumber))})
	}
	return notices, nil
}
