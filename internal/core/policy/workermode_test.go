package policy

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/core/mode"
)

func TestWorkerModeOnlyLowers(t *testing.T) {
	for _, tc := range []struct {
		lead mode.Mode
		env  string
		want mode.Mode
	}{{mode.Full, "plan", mode.Plan}, {mode.Edits, "full", mode.Edits}, {mode.Ask, "", mode.Ask}, {mode.Full, "bogus", mode.Ask}, {mode.Plan, "full", mode.Plan}} {
		if got := WorkerMode(tc.lead, tc.env); got != tc.want {
			t.Errorf("WorkerMode(%s,%q)=%s want %s", tc.lead, tc.env, got, tc.want)
		}
	}
}
