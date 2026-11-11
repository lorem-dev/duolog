package config

import (
	"path/filepath"
	"slices"
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
		writeConfig(t, userConfigPath(t), "workers:\n  compact_window: "+c.text+"\n")
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
	// A plan the defaults do not list, or none read, gets the smallest value.
	for plan, want := range map[string]int{"pro": 300000, "max": 500000, "ultra": 700000, "free": 300000, "": 300000} {
		if got := cfg.Workers.Window(plan); got != want {
			t.Errorf("auto on %q: got %d, want %d", plan, got, want)
		}
	}
	// A person's map replaces single keys.
	writeConfig(t, userConfigPath(t), "workers:\n  compact_ladder:\n    pro: 400000\n    team: 0\n")
	cfg, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	// A listed 0 is the person's choice of Claude's own default; the fallback
	// is the smallest value above 0.
	for plan, want := range map[string]int{"pro": 400000, "max": 500000, "team": 0, "enterprise": 400000} {
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

// TestSharedLayersOnlyLowerWorkers checks a committed project layer and a
// tracked local layer: they may lower the compact windows and turn plugins
// off, and every other workers value is dropped with a notice.
func TestSharedLayersOnlyLowerWorkers(t *testing.T) {
	for _, c := range []struct {
		name, user, shared string
		window             string
		ladder             map[string]int
		plugins            bool
		notices            []string
	}{
		{name: "raise window over auto", shared: "compact_window: 1000000",
			window: "auto", notices: []string{"workers.compact_window"}},
		{name: "lower window under auto", shared: "compact_window: 200000", window: "200000"},
		{name: "window between ladder values", shared: "compact_window: 400000",
			window: "auto", notices: []string{"workers.compact_window"}},
		{name: "0 over auto", shared: "compact_window: 0",
			window: "auto", notices: []string{"workers.compact_window"}},
		{name: "auto over a number", user: "compact_window: 400000", shared: "compact_window: auto",
			window: "400000", notices: []string{"workers.compact_window"}},
		{name: "auto over 0", user: "compact_window: 0", shared: "compact_window: auto", window: "auto"},
		{name: "lower a number", user: "compact_window: 400000", shared: "compact_window: 300000", window: "300000"},
		{name: "raise a number", user: "compact_window: 400000", shared: "compact_window: 500000",
			window: "400000", notices: []string{"workers.compact_window"}},
		{name: "any number over 0", user: "compact_window: 0", shared: "compact_window: 900000", window: "900000"},
		// A plan the base does not list gets the base's fallback, its
		// smallest value, so a new plan may only go under that.
		{name: "ladder", shared: "compact_ladder: {pro: 200000, max: 600000, ultra: 0, team: 400000, free: 0, edu: 150000}",
			window: "auto", ladder: map[string]int{"pro": 200000, "max": 500000, "ultra": 700000, "team": 0, "free": 0, "edu": 150000},
			notices: []string{"workers.compact_ladder.free", "workers.compact_ladder.max", "workers.compact_ladder.team", "workers.compact_ladder.ultra"}},
		{name: "plugins on", shared: "inherit_plugins: true",
			window: "auto", notices: []string{"workers.inherit_plugins"}},
		{name: "plugins off", user: "inherit_plugins: true", shared: "inherit_plugins: false", window: "auto"},
		{name: "plugins on again", user: "inherit_plugins: true", shared: "inherit_plugins: true", window: "auto", plugins: true},
	} {
		for _, layer := range []string{"project", "tracked local"} {
			t.Run(c.name+"/"+layer, func(t *testing.T) {
				root := isolatedRoot(t)
				if c.user != "" {
					writeConfig(t, userConfigPath(t), "workers:\n  "+c.user+"\n")
				}
				path := filepath.Join(root, ".duolog", "config.yaml")
				if layer != "project" {
					path = filepath.Join(root, ".duolog", "config.local.yaml")
				}
				writeConfig(t, path, "workers:\n  "+c.shared+"\n")
				if layer != "project" {
					gitRepo(t, root, []string{"add", "-f", ".duolog/config.local.yaml"})
				}
				cfg, err := Load(root)
				if err != nil {
					t.Fatal(err)
				}
				if cfg.Workers.CompactWindow != c.window || cfg.Workers.InheritPlugins != c.plugins {
					t.Fatalf("workers = %+v", cfg.Workers)
				}
				for plan, want := range c.ladder {
					if got := cfg.Workers.CompactLadder[plan]; got != want {
						t.Errorf("ladder %s = %d, want %d", plan, got, want)
					}
				}
				var got []string
				for _, n := range cfg.Notices {
					if strings.HasPrefix(n.Key, "workers.") {
						got = append(got, n.Key)
					}
				}
				slices.Sort(got)
				if !slices.Equal(got, c.notices) {
					t.Fatalf("notices = %v, want %v (%+v)", got, c.notices, cfg.Notices)
				}
			})
		}
	}
}

func TestLeadCompactNotesDefaultsToTrue(t *testing.T) {
	root := isolatedRoot(t)
	cfg, err := Load(root)
	if err != nil || !cfg.Lead.CompactNotes {
		t.Fatalf("default: %+v %v", cfg.Lead, err)
	}
	writeConfig(t, filepath.Join(root, ".duolog/config.yaml"), "lead:\n  compact_notes: false\n")
	cfg, err = Load(root)
	if err != nil || cfg.Lead.CompactNotes {
		t.Fatalf("off: %+v %v", cfg.Lead, err)
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
