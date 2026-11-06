package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCalibrationDefaults(t *testing.T) {
	cfg, err := Load(isolatedRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Calibration
	if c.MaxWeeklyShare != 0.10 || c.MaxWindowShare != 0.5 || c.CellMaxMinutes != 45 || c.CellMaxTokens != 1500000 || c.CellMaxTurns != 100 || c.HistoryDays != 90 {
		t.Fatal(c)
	}
	f := c.Filter
	if f.MinFiles != 1 || f.MaxFiles != 20 || f.MinLines != 10 || f.MaxLines != 400 || f.LargeMaxFiles != 40 || f.LargeMaxLines != 1500 {
		t.Fatal(f)
	}
}

func TestCalibrationValidation(t *testing.T) {
	// project config values -> error substring
	cases := map[string]string{
		"calibration: {max_weekly_share: 0}":                    "calibration.max_weekly_share",
		"calibration: {max_weekly_share: 1.5}":                  "calibration.max_weekly_share",
		"calibration: {max_window_share: 0.6}":                  "calibration.max_window_share",
		"calibration: {max_weekly_share: .nan}":                 "calibration.max_weekly_share",
		"calibration: {max_weekly_share: .inf}":                 "calibration.max_weekly_share",
		"calibration: {max_weekly_share: -.inf}":                "calibration.max_weekly_share",
		"calibration: {max_window_share: .nan}":                 "calibration.max_window_share",
		"calibration: {max_window_share: .inf}":                 "calibration.max_window_share",
		"calibration: {max_window_share: -.inf}":                "calibration.max_window_share",
		"calibration: {filter: {min_lines: 50, max_lines: 40}}": "calibration.filter",
	}
	for text, want := range cases {
		root := isolatedRoot(t)
		writeConfig(t, filepath.Join(root, ".duolog/config.yaml"), text+"\n")
		if _, err := Load(root); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want an error with %q", text, err, want)
		}
	}
	root := isolatedRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog/config.yaml"), "calibration: {max_weekly_share: 0.05}\n")
	cfg, err := Load(root)
	if err != nil || cfg.Calibration.MaxWeeklyShare != 0.05 {
		t.Fatalf("%+v %v", cfg, err)
	}
}

func TestCalibrationSharedCapsOnlyLower(t *testing.T) {
	// A committed config that raises a cap is ignored, and Load says so.
	root := isolatedRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog/config.yaml"), "calibration: {max_weekly_share: 0.5, max_window_share: 0.3}\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Calibration
	if c.MaxWeeklyShare != 0.10 || c.MaxWindowShare != 0.3 {
		t.Fatalf("caps %v weekly, %v window; want the default 0.1 weekly and the lower shared 0.3 window", c.MaxWeeklyShare, c.MaxWindowShare)
	}
	if len(c.Ignored) != 1 || !strings.Contains(c.Ignored[0], "calibration.max_weekly_share 0.5 in .duolog/config.yaml is ignored") {
		t.Fatalf("Ignored = %q, want the weekly share", c.Ignored)
	}

	// The person's own files may raise it.
	writeConfig(t, filepath.Join(root, ".duolog/config.local.yaml"), "calibration: {max_weekly_share: 0.2}\n")
	if cfg, err = Load(root); err != nil || cfg.Calibration.MaxWeeklyShare != 0.2 || len(cfg.Calibration.Ignored) != 0 {
		t.Fatalf("with a local cap: %+v, %v; want 0.2 and nothing ignored", cfg.Calibration, err)
	}
	userDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, ".duolog/config.local.yaml")); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, filepath.Join(userDir, "duolog", "config.yaml"), "calibration: {max_weekly_share: 0.6}\n")
	if cfg, err = Load(root); err != nil || cfg.Calibration.MaxWeeklyShare != 0.5 || len(cfg.Calibration.Ignored) != 0 {
		t.Fatalf("with a user cap of 0.6: %+v, %v; want the lower shared 0.5", cfg.Calibration, err)
	}
}
