package config

import (
	"fmt"
	"strconv"

	"github.com/lorem-dev/duolog/internal/core/config"
)

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
