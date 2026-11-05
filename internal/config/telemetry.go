package config

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// telemetryEnv set to off turns local telemetry off for every project. No
// value turns it on.
const telemetryEnv = "DUOLOG_TELEMETRY"

// maxRetentionDays bounds how long any telemetry record lives.
const maxRetentionDays = 30

type Telemetry struct {
	Enabled         bool `yaml:"enabled" jsonschema_description:"Keep local telemetry on this machine; defaults to true. User level; a project may set false to stop its own run, gate and peer events, and DUOLOG_TELEMETRY=off turns it off everywhere."`
	RetentionDays   int  `yaml:"retention_days" jsonschema_description:"Days a telemetry record lives, 1 to 30; defaults to 30. User level only; project layers cannot change it." jsonschema:"minimum=1,maximum=30"`
	PruneEveryHours int  `yaml:"prune_every_hours" jsonschema_description:"Hours between prunes triggered by a telemetry write; defaults to 6. User level only." jsonschema:"minimum=1"`
	MaxMegabytes    int  `yaml:"max_megabytes" jsonschema_description:"Size cap of the whole telemetry store in megabytes, oldest segments removed first; defaults to 16. User level only." jsonschema:"minimum=1"`
	ProjectEnabled  bool `yaml:"-" jsonschema:"-"`
}

// keepTelemetry returns the user-level section with only the disabling rules
// applied: retention, caps and the prune interval stay user-wide, so pruning
// never depends on which project runs it. A project can stop its own
// partition but not account events, and the environment can only turn it off.
func keepTelemetry(user Telemetry, projectOff, envOff bool) Telemetry {
	kept := user
	if envOff {
		kept.Enabled = false
	}
	kept.ProjectEnabled = kept.Enabled && !projectOff
	return kept
}

// userTelemetry reads the telemetry section merged from the embedded defaults
// and the user layer, before any project layer.
func userTelemetry(merged *yaml.Node) (Telemetry, error) {
	var section struct {
		Telemetry Telemetry `yaml:"telemetry"`
	}
	if err := merged.Decode(&section); err != nil {
		return Telemetry{}, fmt.Errorf("user config: %w", err)
	}
	return section.Telemetry, nil
}

// telemetryDisabled reports whether a project layer sets telemetry.enabled to
// false. An omitted field leaves it as it is.
func telemetryDisabled(data []byte, source string) (bool, error) {
	var layer struct {
		Telemetry struct {
			Enabled *bool `yaml:"enabled"`
		} `yaml:"telemetry"`
	}
	if err := yaml.Unmarshal(data, &layer); err != nil {
		return false, fmt.Errorf("%s: %w", source, err)
	}
	return layer.Telemetry.Enabled != nil && !*layer.Telemetry.Enabled, nil
}

func telemetryEnvOff() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(telemetryEnv)), "off")
}

func validateTelemetry(t Telemetry) error {
	if t.RetentionDays < 1 || t.RetentionDays > maxRetentionDays {
		return fmt.Errorf("telemetry.retention_days: must be 1 to %d", maxRetentionDays)
	}
	if t.PruneEveryHours < 1 || int64(t.PruneEveryHours) > math.MaxInt64/int64(time.Hour) {
		return fmt.Errorf("telemetry.prune_every_hours: must be greater than zero and fit a duration")
	}
	if t.MaxMegabytes < 1 || int64(t.MaxMegabytes) > math.MaxInt64>>20 {
		return fmt.Errorf("telemetry.max_megabytes: must be greater than zero and fit a byte count")
	}
	return nil
}
