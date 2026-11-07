package config

import (
	"fmt"
	"math"
	"slices"
	"strconv"

	"go.yaml.in/yaml/v3"

	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/config"
)

// validatePaidRuns checks the merged paid settings. Interval checks are
// negated so that NaN, which compares false with every bound, cannot pass.
func validatePaidRuns(cfg *config.Config, agents []agent.Descriptor) error {
	p := cfg.PaidRuns
	if !(p.UsableShare > 0 && p.UsableShare <= 1) {
		return fmt.Errorf("paid_runs.usable_share: must be above 0 and at most 1")
	}
	for _, d := range agents {
		name := d.Name
		if _, ok := p.Agents[name]; !ok {
			continue
		}
		a, key := p.Agent(name), "paid_runs."+name
		if !(a.DailyCapUSD >= 0 && a.DailyCapUSD <= math.MaxFloat64) {
			return fmt.Errorf("%s.daily_cap_usd: must be a finite number of USD, 0 or more", key)
		}
		if a.Allow && a.DailyCapUSD <= 0 {
			return fmt.Errorf("%s.allow: needs %s.daily_cap_usd above 0", key, key)
		}
		if a.Calibrate && !a.Allow {
			return fmt.Errorf("%s.calibrate: needs %s.allow", key, key)
		}
		if accountRank(a.Account) < 0 {
			return fmt.Errorf("%s.account: invalid value %q (want auto, personal, workspace)", key, a.Account)
		}
	}
	return nil
}

// accountRank orders account kinds from the loosest to the strictest share.
func accountRank(account string) int {
	return slices.Index([]string{"personal", "auto", "workspace"}, account)
}

const (
	projectFile = ".duolog/config.yaml"
	localFile   = ".duolog/config.local.yaml"
	// trackedLocal names a local file that Git tracks in notices: it reaches
	// everyone who clones the repository, like the project file.
	trackedLocal = localFile + ", which Git tracks,"
	// outsideLocal names a local file outside a Git checkout in notices:
	// nothing proves that it is the person's own.
	outsideLocal = localFile + ", outside a Git checkout,"
)

// restrictProjectPaid removes from the project layer each paid_runs value
// that would widen what the layers below allow, before the layer is merged.
// The committed project file reaches everyone who clones the repository, so
// it may turn paid runs off, lower a cap or the usable share, or choose a
// stricter account kind, but never the reverse. Its env list and endpoint
// map may only keep entries the layers below trust: the endpoints of base
// shrink to the common entries, and the env list replaces base's anyway.
// Only keys the layer sets are compared, so an omitted key changes nothing.
// Both nodes are resolved mappings; aliases and merge keys are already
// expanded, so every value is compared where it applies. projectFile names
// the shared file in notices.
func restrictProjectPaid(base, layer *yaml.Node, projectFile string) ([]config.ConfigNotice, error) {
	paid := mappingValue(layer, "paid_runs")
	if paid == nil {
		return nil, nil
	}
	basePaid := mappingValue(base, "paid_runs")
	var notices []config.ConfigNotice
	ignore := func(key, value, applies string) {
		notices = append(notices, config.ConfigNotice{Key: key, Reason: fmt.Sprintf(
			"%s in %s is ignored: a shared config may only make paid runs stricter; %s applies",
			value, projectFile, applies)})
	}
	var err error
	paid.Content, err = keepEntries(paid.Content, func(key string, value *yaml.Node) (bool, error) {
		if key != "usable_share" {
			return true, nil
		}
		return keepLower(mappingValue(basePaid, key), value, "paid_runs."+key, ignore)
	})
	if err != nil {
		return nil, err
	}
	for i := 0; i+1 < len(paid.Content); i += 2 {
		agent, settings := paid.Content[i].Value, paid.Content[i+1]
		if agent == "usable_share" {
			continue
		}
		baseAgent, prefix := mappingValue(basePaid, agent), "paid_runs."+agent+"."
		settings.Content, err = keepEntries(settings.Content, func(key string, value *yaml.Node) (bool, error) {
			baseValue := mappingValue(baseAgent, key)
			switch key {
			case "allow", "calibrate":
				var on, baseOn bool
				if err := decodeOptional(value, &on); err != nil {
					return false, err
				}
				if err := decodeOptional(baseValue, &baseOn); err != nil {
					return false, err
				}
				if on && !baseOn {
					ignore(prefix+key, "true", "false")
					return false, nil
				}
			case "daily_cap_usd":
				return keepLower(baseValue, value, prefix+key, ignore)
			case "account":
				var account, baseAccount string
				if err := decodeOptional(value, &account); err != nil {
					return false, err
				}
				if err := decodeOptional(baseValue, &baseAccount); err != nil {
					return false, err
				}
				if accountRank(account) < accountRank(baseAccount) {
					ignore(prefix+key, account, baseAccount)
					return false, nil
				}
			case "env":
				value.Content = intersectEnv(baseValue, value, func(name string) {
					notices = append(notices, config.ConfigNotice{Key: prefix + key, Reason: fmt.Sprintf(
						"%s in %s is ignored: a shared config may only remove variables from the paid env",
						name, projectFile)})
				})
			case "subscription_endpoints":
				intersectEndpoints(baseValue, value, func(name string) {
					notices = append(notices, config.ConfigNotice{Key: prefix + key + "." + name, Reason: fmt.Sprintf(
						"%s in %s is ignored: a shared config may not trust an endpoint the user config does not",
						name, projectFile)})
				})
			}
			return true, nil
		})
		if err != nil {
			return nil, err
		}
	}
	return notices, nil
}

// keepEntries keeps the mapping entries for which keep returns true.
func keepEntries(content []*yaml.Node, keep func(string, *yaml.Node) (bool, error)) ([]*yaml.Node, error) {
	var kept []*yaml.Node
	for i := 0; i+1 < len(content); i += 2 {
		ok, err := keep(content[i].Value, content[i+1])
		if err != nil {
			return nil, err
		}
		if ok {
			kept = append(kept, content[i], content[i+1])
		}
	}
	return kept, nil
}

// keepLower keeps a number that is not above the one below it. A missing
// base counts as 0, so nothing above 0 passes.
func keepLower(base, value *yaml.Node, key string, ignore func(string, string, string)) (bool, error) {
	var number, baseNumber float64
	if err := decodeOptional(value, &number); err != nil {
		return false, err
	}
	if err := decodeOptional(base, &baseNumber); err != nil {
		return false, err
	}
	if number > baseNumber {
		ignore(key, formatNumber(number), formatNumber(baseNumber))
		return false, nil
	}
	return true, nil
}

// intersectEnv returns the names of value that base lists too, in value's
// order, and reports each other name.
func intersectEnv(base, value *yaml.Node, drop func(string)) []*yaml.Node {
	var trusted []string
	if base != nil {
		for _, item := range base.Content {
			trusted = append(trusted, item.Value)
		}
	}
	var kept []*yaml.Node
	for _, item := range value.Content {
		if slices.Contains(trusted, item.Value) {
			kept = append(kept, item)
		} else {
			drop(item.Value)
		}
	}
	return kept
}

// intersectEndpoints keeps the entries that base and value both have with
// the same URL, in both, and reports each other entry of value by name.
// Mappings merge by key, so base must shrink too for value to narrow it.
func intersectEndpoints(base, value *yaml.Node, drop func(string)) {
	same := func(name, url string) bool {
		trusted := mappingValue(base, name)
		return trusted != nil && trusted.Value == url
	}
	value.Content, _ = keepEntries(value.Content, func(name string, url *yaml.Node) (bool, error) {
		if same(name, url.Value) {
			return true, nil
		}
		drop(name)
		return false, nil
	})
	if base != nil {
		base.Content, _ = keepEntries(base.Content, func(name string, url *yaml.Node) (bool, error) {
			kept := mappingValue(value, name)
			return kept != nil && kept.Value == url.Value, nil
		})
	}
}

// mappingValue returns the value of key in a mapping node, or nil.
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// decodeOptional decodes node into v and leaves v unchanged for nil.
func decodeOptional(node *yaml.Node, v any) error {
	if node == nil {
		return nil
	}
	if err := node.Decode(v); err != nil {
		return fmt.Errorf("line %d: %w", node.Line, err)
	}
	return nil
}

func formatNumber(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
