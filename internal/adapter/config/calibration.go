package config

import (
	"fmt"
	"strconv"

	"go.yaml.in/yaml/v3"

	"github.com/lorem-dev/duolog/internal/core/config"
)

// validateCalibration checks the caps and filter bounds. A weekly share
// above the default 0.10 is valid: only the person edits the config. The
// share checks negate membership of the interval, so NaN, which compares
// false with every bound, cannot pass.
func validateCalibration(cfg *config.Config) error {
	c := cfg.Calibration
	if !(c.MaxWeeklyShare > 0 && c.MaxWeeklyShare <= 1) {
		return fmt.Errorf("calibration.max_weekly_share: must be above 0 and at most 1")
	}
	if !(c.MaxWindowShare > 0 && c.MaxWindowShare <= 0.5) {
		return fmt.Errorf("calibration.max_window_share: must be above 0 and at most 0.5")
	}
	if c.CellMaxMinutes <= 0 {
		return fmt.Errorf("calibration.cell_max_minutes: must be greater than zero")
	}
	if c.CellMaxTokens <= 0 {
		return fmt.Errorf("calibration.cell_max_tokens: must be greater than zero")
	}
	if c.CellMaxTurns <= 0 {
		return fmt.Errorf("calibration.cell_max_turns: must be greater than zero")
	}
	if c.HistoryDays <= 0 {
		return fmt.Errorf("calibration.history_days: must be greater than zero")
	}
	f := c.Filter
	if f.MinFiles < 1 || f.MaxFiles < f.MinFiles || f.LargeMaxFiles < f.MaxFiles {
		return fmt.Errorf("calibration.filter: want 1 <= min_files <= max_files <= large_max_files")
	}
	if f.MinLines < 0 || f.MaxLines < f.MinLines || f.LargeMaxLines < f.MaxLines {
		return fmt.Errorf("calibration.filter: want 0 <= min_lines <= max_lines <= large_max_lines")
	}
	return nil
}

// layerCaps are the calibration caps one layer sets; nil for one it leaves.
type layerCaps struct {
	Calibration struct {
		MaxWeeklyShare *float64 `yaml:"max_weekly_share"`
		MaxWindowShare *float64 `yaml:"max_window_share"`
	} `yaml:"calibration"`
}

// lowerSharedCaps keeps a cap of the shared .duolog/config.yaml only when it
// is not above the embedded and user layers: a committed config may lower
// what calibration spends, never raise it for everyone who pulls it. The
// local layer is the person's own and still wins, unless it counts as shared
// (localShared names it, as sharedLocal does): then it may only lower the
// caps too. Each layer is its bytes, nil when the file does not exist.
func lowerSharedCaps(cfg *config.Config, user, shared, local []byte, localLabel string) error {
	localShared := localLabel != ""
	var layers [4]layerCaps
	for i, data := range [][]byte{config.DefaultYAML(), user, shared, local} {
		if err := yaml.Unmarshal(data, &layers[i]); err != nil {
			return err
		}
	}
	caps := []struct {
		name  string
		value *float64
		layer func(layerCaps) *float64
	}{
		{"max_weekly_share", &cfg.Calibration.MaxWeeklyShare, func(l layerCaps) *float64 { return l.Calibration.MaxWeeklyShare }},
		{"max_window_share", &cfg.Calibration.MaxWindowShare, func(l layerCaps) *float64 { return l.Calibration.MaxWindowShare }},
	}
	for _, c := range caps {
		base := c.layer(layers[0])
		if user := c.layer(layers[1]); user != nil {
			base = user
		}
		if base == nil || !localShared && c.layer(layers[3]) != nil {
			continue
		}
		// Replay the shared layers over the person's value: a shared one
		// applies only when it does not raise the cap. NaN never passes.
		value, changed := *base, false
		for _, l := range []struct {
			cap    *float64
			file   string
			shared bool
		}{{c.layer(layers[2]), projectFile, true}, {c.layer(layers[3]), localLabel, localShared}} {
			switch {
			case l.cap == nil:
			case !l.shared || *l.cap <= value:
				value = *l.cap
			default:
				changed = true
				cfg.Calibration.Ignored = append(cfg.Calibration.Ignored, fmt.Sprintf(
					"calibration.%s %s in %s is ignored: a shared config may only lower calibration caps; %s applies",
					c.name, strconv.FormatFloat(*l.cap, 'f', -1, 64), l.file, strconv.FormatFloat(value, 'f', -1, 64)))
			}
		}
		if changed {
			*c.value = value
		}
	}
	return nil
}
