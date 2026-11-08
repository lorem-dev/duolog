package config

import (
	"fmt"
	"math"
	"strconv"

	"go.yaml.in/yaml/v3"

	"github.com/lorem-dev/duolog/internal/core/config"
)

// restrictProjectWorkers removes from a shared layer each workers value that
// would raise what a Claude worker sends per request over the layers below:
// a larger compact window, or plugins turned on. The layer may lower
// compact_window and compact_ladder values and set inherit_plugins to false.
// A window of 0 leaves Claude's own default, which counts as no limit; auto
// counts as each plan's ladder value. A value out of range is kept, so that
// validateCompact refuses it.
func restrictProjectWorkers(base, layer *yaml.Node, projectFile string) ([]config.ConfigNotice, error) {
	workers := mappingValue(layer, "workers")
	if workers == nil || workers.Kind != yaml.MappingNode {
		return nil, nil
	}
	r := workersRule{base: mappingValue(base, "workers"), file: projectFile}
	r.ladder = ladderLimits(mappingValue(r.base, "compact_ladder"))
	var err error
	workers.Content, err = keepEntries(workers.Content, func(key string, value *yaml.Node) (bool, error) {
		switch key {
		case "inherit_plugins":
			return r.plugins(value)
		case "compact_window":
			return r.window(value), nil
		case "compact_ladder":
			r.lowerLadder(value)
		}
		return true, nil
	})
	return r.notices, err
}

// workersRule holds the base values that restrictProjectWorkers compares a
// shared layer with, and the notices it gives.
type workersRule struct {
	base    *yaml.Node
	ladder  map[string]int
	file    string
	notices []config.ConfigNotice
}

func (r *workersRule) lowerOnly(key, value, applies string) {
	r.notices = append(r.notices, config.ConfigNotice{Key: key, Reason: fmt.Sprintf(
		"%s in %s is ignored: a shared config may only lower the worker compact window; %s applies",
		value, r.file, applies)})
}

// plugins keeps inherit_plugins unless it turns plugins on over the base.
func (r *workersRule) plugins(value *yaml.Node) (bool, error) {
	var on, baseOn bool
	if err := decodeOptional(value, &on); err != nil {
		return false, err
	}
	if err := decodeOptional(mappingValue(r.base, "inherit_plugins"), &baseOn); err != nil {
		return false, err
	}
	if on && !baseOn {
		r.notices = append(r.notices, config.ConfigNotice{Key: "workers.inherit_plugins", Reason: fmt.Sprintf(
			"true in %s is ignored: a shared config may only turn worker plugins off; false applies", r.file)})
		return false, nil
	}
	return true, nil
}

// window keeps compact_window unless it allows a larger context than the base.
func (r *workersRule) window(value *yaml.Node) bool {
	baseValue := "auto"
	if v := mappingValue(r.base, "compact_window"); v != nil {
		baseValue = v.Value
	}
	if value.Kind == yaml.ScalarNode && !windowLowers(value.Value, baseValue, r.ladder) {
		r.lowerOnly("workers.compact_window", value.Value, baseValue)
		return false
	}
	return true
}

// lowerLadder removes each compact_ladder value larger than the base's. A plan
// the base does not list gets the base's fallback window, its smallest value
// above 0, so a new plan may not go above that.
func (r *workersRule) lowerLadder(ladder *yaml.Node) {
	if ladder.Kind != yaml.MappingNode {
		return
	}
	fallback := math.MaxInt
	for _, limit := range r.ladder {
		fallback = min(fallback, limit)
	}
	ladder.Content, _ = keepEntries(ladder.Content, func(plan string, n *yaml.Node) (bool, error) {
		limit, ok := windowLimit(n.Value)
		if !ok || n.Kind != yaml.ScalarNode {
			return true, nil
		}
		baseLimit, listed := r.ladder[plan]
		if !listed {
			baseLimit = fallback
		}
		if limit > baseLimit {
			r.lowerOnly("workers.compact_ladder."+plan, n.Value, windowText(baseLimit))
			return false, nil
		}
		return true, nil
	})
}

// windowLimit reads a valid compact window as the context size it allows,
// with 0, Claude's own default, as no limit.
func windowLimit(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil || !config.ValidCompactWindow(n) {
		return 0, false
	}
	if n == 0 {
		return math.MaxInt, true
	}
	return n, true
}

// windowText is the setting for a limit from windowLimit.
func windowText(limit int) string {
	if limit == math.MaxInt {
		return "0"
	}
	return strconv.Itoa(limit)
}

// ladderLimits reads the valid values of a compact_ladder mapping.
func ladderLimits(ladder *yaml.Node) map[string]int {
	limits := map[string]int{}
	if ladder == nil || ladder.Kind != yaml.MappingNode {
		return limits
	}
	for i := 0; i+1 < len(ladder.Content); i += 2 {
		if limit, ok := windowLimit(ladder.Content[i+1].Value); ok {
			limits[ladder.Content[i].Value] = limit
		}
	}
	return limits
}

// windowLowers reports that compact_window value allows no larger context
// than base for any plan. An invalid value, or an invalid base, is left to
// validateCompact.
func windowLowers(value, base string, baseLadder map[string]int) bool {
	if value == base {
		return true
	}
	baseLimit, baseOK := windowLimit(base)
	if value == "auto" {
		// A plan the ladder lists with 0 gets Claude's own default, and the
		// ladder's largest value may exceed any number, so auto only stays
		// under no limit.
		return !baseOK && base != "auto" || baseLimit == math.MaxInt
	}
	limit, ok := windowLimit(value)
	if !ok || !baseOK && base != "auto" {
		return true
	}
	if base != "auto" {
		return limit <= baseLimit
	}
	for _, planLimit := range baseLadder {
		if limit > planLimit {
			return false
		}
	}
	return true
}

// validateCompact checks workers.compact_window and each value of
// workers.compact_ladder: auto (the window only), 0, or 100000 to 1000000.
func validateCompact(w config.Workers) error {
	want := fmt.Sprintf("0 or between %d and %d", config.MinCompactWindow, config.MaxCompactWindow)
	if w.CompactWindow != "auto" {
		n, err := strconv.Atoi(w.CompactWindow)
		if err != nil || !config.ValidCompactWindow(n) {
			return fmt.Errorf("workers.compact_window must be auto, or %s, not %q", want, w.CompactWindow)
		}
	}
	for plan, n := range w.CompactLadder {
		if !config.ValidCompactWindow(n) {
			return fmt.Errorf("workers.compact_ladder.%s must be %s, not %d", plan, want, n)
		}
	}
	return nil
}
