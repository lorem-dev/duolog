package config

import "testing"

// auto takes a listed plan's value, and gives a plan the ladder does not
// list, or one the read could not name, the smallest window above 0 the
// ladder holds: a bounded window, never Claude's own default.
func TestWindowFallsBackToTheSmallestLadderValue(t *testing.T) {
	ladder := map[string]int{"pro": 250000, "max": 500000, "ultra": 700000, "team": 0}
	for _, c := range []struct {
		window string
		ladder map[string]int
		plan   string
		want   int
	}{
		{"auto", ladder, "max", 500000},
		{"auto", ladder, "ultra", 700000},
		{"auto", ladder, "enterprise", 250000},
		{"auto", ladder, "", 250000},
		{"auto", ladder, "team", 0},
		{"auto", map[string]int{"max": 600000, "pro": 300000}, "", 300000},
		{"auto", map[string]int{"team": 0}, "", 0},
		{"auto", nil, "", 0},
		{"0", ladder, "", 0},
		{"400000", ladder, "", 400000},
		{"", ladder, "", 0},
	} {
		w := Workers{CompactWindow: c.window, CompactLadder: c.ladder}
		if got := w.Window(c.plan); got != c.want {
			t.Errorf("window %q, ladder %v, plan %q: got %d, want %d", c.window, c.ladder, c.plan, got, c.want)
		}
	}
}
