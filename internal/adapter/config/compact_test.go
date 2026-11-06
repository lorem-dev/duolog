package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkersCompactWindow(t *testing.T) {
	root := isolatedRoot(t)
	cfg, err := Load(root)
	if err != nil || cfg.Workers.CompactWindow != "auto" {
		t.Fatalf("default: %+v %v", cfg.Workers, err)
	}
	for _, c := range []struct {
		text string
		want string
		bad  bool
	}{
		{"auto", "auto", false}, {"0", "0", false}, {"100000", "100000", false}, {"250000", "250000", false},
		{"1000000", "1000000", false},
		{"99999", "", true}, {"1000001", "", true}, {"-1", "", true}, {"1", "", true}, {"soon", "", true},
	} {
		writeConfig(t, filepath.Join(root, ".duolog/config.yaml"), "workers:\n  compact_window: "+c.text+"\n")
		cfg, err := Load(root)
		if c.bad {
			if err == nil || !strings.Contains(err.Error(), "workers.compact_window") {
				t.Fatalf("%s: want a workers.compact_window error, got %v", c.text, err)
			}
			continue
		}
		if err != nil || cfg.Workers.CompactWindow != c.want {
			t.Fatalf("%s: %+v %v", c.text, cfg.Workers, err)
		}
	}
}

func TestWorkersCompactLadder(t *testing.T) {
	root := isolatedRoot(t)
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for plan, want := range map[string]int{"pro": 250000, "max": 500000, "ultra": 700000, "free": 0, "": 0} {
		if got := cfg.Workers.Window(plan); got != want {
			t.Errorf("auto on %q: got %d, want %d", plan, got, want)
		}
	}
	// A person's map replaces single keys.
	writeConfig(t, filepath.Join(root, ".duolog/config.yaml"), "workers:\n  compact_ladder:\n    pro: 300000\n    team: 0\n")
	cfg, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for plan, want := range map[string]int{"pro": 300000, "max": 500000, "team": 0} {
		if got := cfg.Workers.Window(plan); got != want {
			t.Errorf("merged ladder on %q: got %d, want %d", plan, got, want)
		}
	}
	for _, text := range []string{"50000", "1000001", "-5"} {
		writeConfig(t, filepath.Join(root, ".duolog/config.yaml"), "workers:\n  compact_ladder:\n    pro: "+text+"\n")
		if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "workers.compact_ladder.pro") {
			t.Fatalf("%s: want a workers.compact_ladder.pro error, got %v", text, err)
		}
	}
}

func TestWorkersFixedWindowIgnoresThePlan(t *testing.T) {
	root := isolatedRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog/config.yaml"), "workers:\n  compact_window: 150000\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range []string{"pro", "max", "", "free"} {
		if got := cfg.Workers.Window(plan); got != 150000 {
			t.Errorf("plan %q: got %d", plan, got)
		}
	}
}
