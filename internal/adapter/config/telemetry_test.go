package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
)

func telemetryRoot(t *testing.T) (root, userPath string) {
	t.Helper()
	root = isolatedRoot(t)
	t.Setenv("DUOLOG_TELEMETRY", "")
	userDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(userDir, "duolog", "config.yaml")
}

func TestTelemetryDefaults(t *testing.T) {
	root, _ := telemetryRoot(t)
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	want := config.Telemetry{Enabled: true, RetentionDays: 30, PruneEveryHours: 6, MaxMegabytes: 16, ProjectEnabled: true}
	if cfg.Telemetry != want {
		t.Fatalf("got %+v, want %+v", cfg.Telemetry, want)
	}
}

func TestTelemetryLayers(t *testing.T) {
	defaults := config.Telemetry{Enabled: true, RetentionDays: 30, PruneEveryHours: 6, MaxMegabytes: 16, ProjectEnabled: true}
	for _, tc := range []struct {
		name               string
		user, project, loc string
		env                string
		want               config.Telemetry
	}{
		{name: "absent fields keep defaults", user: "telemetry:\n  retention_days: 10\n", project: "telemetry: {}\n",
			want: config.Telemetry{Enabled: true, RetentionDays: 10, PruneEveryHours: 6, MaxMegabytes: 16, ProjectEnabled: true}},
		{name: "user off with project true", user: "telemetry:\n  enabled: false\n", project: "telemetry:\n  enabled: true\n",
			want: config.Telemetry{Enabled: false, RetentionDays: 30, PruneEveryHours: 6, MaxMegabytes: 16, ProjectEnabled: false}},
		{name: "project false keeps account events", project: "telemetry:\n  enabled: false\n",
			want: config.Telemetry{Enabled: true, RetentionDays: 30, PruneEveryHours: 6, MaxMegabytes: 16, ProjectEnabled: false}},
		{name: "project false with local true", project: "telemetry:\n  enabled: false\n", loc: "telemetry:\n  enabled: true\n",
			want: config.Telemetry{Enabled: true, RetentionDays: 30, PruneEveryHours: 6, MaxMegabytes: 16, ProjectEnabled: false}},
		{name: "local false with project true", project: "telemetry:\n  enabled: true\n", loc: "telemetry:\n  enabled: false\n",
			want: config.Telemetry{Enabled: true, RetentionDays: 30, PruneEveryHours: 6, MaxMegabytes: 16, ProjectEnabled: false}},
		{name: "lower layers cannot change maintenance",
			user:    "telemetry:\n  retention_days: 20\n  prune_every_hours: 12\n  max_megabytes: 8\n",
			project: "telemetry:\n  retention_days: 30\n  prune_every_hours: 1\n  max_megabytes: 64\n",
			loc:     "telemetry:\n  retention_days: 5\n  prune_every_hours: 24\n  max_megabytes: 2\n",
			want:    config.Telemetry{Enabled: true, RetentionDays: 20, PruneEveryHours: 12, MaxMegabytes: 8, ProjectEnabled: true}},
		{name: "env off", env: "off",
			want: config.Telemetry{Enabled: false, RetentionDays: 30, PruneEveryHours: 6, MaxMegabytes: 16, ProjectEnabled: false}},
		{name: "env on cannot enable", user: "telemetry:\n  enabled: false\n", env: "on",
			want: config.Telemetry{Enabled: false, RetentionDays: 30, PruneEveryHours: 6, MaxMegabytes: 16, ProjectEnabled: false}},
		{name: "env on keeps defaults", env: "on", want: defaults},
		{name: "env empty keeps defaults", env: "", want: defaults},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, userPath := telemetryRoot(t)
			t.Setenv("DUOLOG_TELEMETRY", tc.env)
			if tc.user != "" {
				writeConfig(t, userPath, tc.user)
			}
			if tc.project != "" {
				writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), tc.project)
			}
			if tc.loc != "" {
				writeConfig(t, filepath.Join(root, ".duolog", "config.local.yaml"), tc.loc)
			}
			cfg, err := Load(root)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Telemetry != tc.want {
				t.Fatalf("got %+v, want %+v", cfg.Telemetry, tc.want)
			}
		})
	}
	for _, layer := range []string{"user", "project"} {
		t.Run("null section in "+layer, func(t *testing.T) {
			root, userPath := telemetryRoot(t)
			path := userPath
			if layer == "project" {
				path = filepath.Join(root, ".duolog", "config.yaml")
			}
			writeConfig(t, path, "telemetry:\n  enabled: null\n")
			if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "null is not allowed") {
				t.Fatalf("got %v, want null rejected", err)
			}
		})
	}
}

func TestTelemetryValidation(t *testing.T) {
	for _, tc := range []struct{ name, yaml, want string }{
		{"retention zero", "telemetry:\n  retention_days: 0\n", "telemetry.retention_days"},
		{"retention over thirty", "telemetry:\n  retention_days: 31\n", "telemetry.retention_days"},
		{"interval zero", "telemetry:\n  prune_every_hours: 0\n", "telemetry.prune_every_hours"},
		{"interval negative", "telemetry:\n  prune_every_hours: -1\n", "telemetry.prune_every_hours"},
		{"interval overflow", "telemetry:\n  prune_every_hours: 9223372036854775807\n", "telemetry.prune_every_hours"},
		{"cap zero", "telemetry:\n  max_megabytes: 0\n", "telemetry.max_megabytes"},
		{"cap negative", "telemetry:\n  max_megabytes: -16\n", "telemetry.max_megabytes"},
		{"cap overflow", "telemetry:\n  max_megabytes: 9223372036854775807\n", "telemetry.max_megabytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, userPath := telemetryRoot(t)
			writeConfig(t, userPath, tc.yaml)
			if _, err := Load(root); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error naming %s", err, tc.want)
			}
		})
	}
	t.Run("bounds accepted", func(t *testing.T) {
		root, userPath := telemetryRoot(t)
		writeConfig(t, userPath, "telemetry:\n  retention_days: 1\n  prune_every_hours: 1\n  max_megabytes: 1\n")
		if _, err := Load(root); err != nil {
			t.Fatal(err)
		}
	})
}
