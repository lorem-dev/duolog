package config

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/lorem-dev/duolog/internal/core/config"
)

// telemetryEnv set to off turns local telemetry off for every project. No
// value turns it on.
const telemetryEnv = "DUOLOG_TELEMETRY"

// maxRetentionDays bounds how long any telemetry record lives.
const maxRetentionDays = 30

// keepTelemetry returns the user-level section with only the disabling rules
// applied: retention, caps and the prune interval stay user-wide, so pruning
// never depends on which project runs it. A project can stop its own
// partition but not account events, and the environment can only turn it off.
func keepTelemetry(user config.Telemetry, projectOff, envOff bool) config.Telemetry {
	kept := user
	if envOff {
		kept.Enabled = false
	}
	kept.ProjectEnabled = kept.Enabled && !projectOff
	return kept
}

// userTelemetry reads the telemetry section merged from the embedded defaults
// and the user layer, before any project layer.
func userTelemetry(merged *yaml.Node) (config.Telemetry, error) {
	var section struct {
		Telemetry config.Telemetry `yaml:"telemetry"`
	}
	if err := merged.Decode(&section); err != nil {
		return config.Telemetry{}, fmt.Errorf("user config: %w", err)
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

func validateTelemetry(t config.Telemetry) error {
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
