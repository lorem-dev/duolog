package config

import (
	"fmt"
	"strconv"

	"go.yaml.in/yaml/v3"
)

type Calibration struct {
	MaxWeeklyShare float64           `yaml:"max_weekly_share" jsonschema_description:"Most of each agent's weekly window the whole calibration may spend, above 0 and at most 1; defaults to 0.10."`
	MaxWindowShare float64           `yaml:"max_window_share" jsonschema_description:"Most of a five-hour window calibration may spend before it pauses until the window resets, above 0 and at most 0.5; defaults to 0.5."`
	CellMaxMinutes int               `yaml:"cell_max_minutes" jsonschema_description:"Time cap for one cell in minutes; a task estimated above it is excluded. Defaults to 45."`
	CellMaxTokens  int64             `yaml:"cell_max_tokens" jsonschema_description:"Token cap for one cell; a task estimated above it is excluded. Defaults to 1500000."`
	CellMaxTurns   int               `yaml:"cell_max_turns" jsonschema_description:"Turn cap for one cell; defaults to 100."`
	HistoryDays    int               `yaml:"history_days" jsonschema_description:"Days of history mined first, widened when a class has fewer than 3 tasks; defaults to 90."`
	Filter         CalibrationFilter `yaml:"filter" jsonschema_description:"Size limits for commits mined as tasks, after dropping generated and vendored files."`
	// Ignored names each cap of the shared .duolog/config.yaml that Load
	// ignored because it was above the layers below it.
	Ignored []string `yaml:"-" jsonschema:"-"`
}

type CalibrationFilter struct {
	MinFiles      int `yaml:"min_files" jsonschema_description:"Fewest changed files for a task; defaults to 1."`
	MaxFiles      int `yaml:"max_files" jsonschema_description:"Most changed files for a task; defaults to 20."`
	MinLines      int `yaml:"min_lines" jsonschema_description:"Fewest changed lines for a task; defaults to 10."`
	MaxLines      int `yaml:"max_lines" jsonschema_description:"Most changed lines for a task; defaults to 400."`
	LargeMaxFiles int `yaml:"large_max_files" jsonschema_description:"Most changed files for the larger stratum, used only when the project's work is large; defaults to 40."`
	LargeMaxLines int `yaml:"large_max_lines" jsonschema_description:"Most changed lines for the larger stratum, used only when the project's work is large; defaults to 1500."`
}

// validateCalibration checks the caps and filter bounds. A weekly share
// above the default 0.10 is valid: only the person edits the config. The
// share checks negate membership of the interval, so NaN, which compares
// false with every bound, cannot pass.
func validateCalibration(cfg *Config) error {
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
// local layer is the person's own and still wins. Each layer is its bytes,
// nil when the file does not exist.
func lowerSharedCaps(cfg *Config, user, shared, local []byte) error {
	var layers [4]layerCaps
	for i, data := range [][]byte{defaultConfig, user, shared, local} {
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
		base, shared := c.layer(layers[0]), c.layer(layers[2])
		if user := c.layer(layers[1]); user != nil {
			base = user
		}
		if shared == nil || base == nil || *shared <= *base || c.layer(layers[3]) != nil {
			continue
		}
		*c.value = *base
		cfg.Calibration.Ignored = append(cfg.Calibration.Ignored, fmt.Sprintf(
			"calibration.%s %s in .duolog/config.yaml is ignored: a shared config may only lower calibration caps; %s applies",
			c.name, strconv.FormatFloat(*shared, 'f', -1, 64), strconv.FormatFloat(*base, 'f', -1, 64)))
	}
	return nil
}
