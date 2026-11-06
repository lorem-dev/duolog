package calibration

import (
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
)

// testTime is a fixed UTC instant, so run ids and dates do not depend on the clock.
var testTime = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

// testCalibration holds the default caps. The admission tests in
// internal/core/budget use the same helpers.
func testCalibration() config.Calibration {
	return config.Calibration{MaxWeeklyShare: 0.10, MaxWindowShare: 0.5}
}

func weekly(used float64) Window {
	return Window{Kind: KindWeekly, UsedPercent: used, ResetsAt: testTime.Add(72 * time.Hour)}
}

func fiveHour(used float64) Window {
	return Window{Kind: KindFiveHour, UsedPercent: used, ResetsAt: testTime.Add(3 * time.Hour)}
}

func stateDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func quality(v float64) *float64 { return &v }

// sampleProject holds two classes whose rows are out of order, so rendering
// has something to sort.
func sampleProject() ProjectPart {
	return ProjectPart{
		Version: 1,
		Survey: SurveyFacts{
			Languages:  []string{"go"},
			Build:      []string{"go build ./..."},
			Test:       []string{"go test ./..."},
			Components: []string{"internal/calibration", "internal/learning"},
		},
		Tasks: []ManifestTask{{
			ID:              "t1",
			Statement:       "Add a Dir function that returns the calibration folder.",
			StatementSource: "plan",
			Class:           "simple_impl",
			Verification:    "hidden_tests",
			Sources:         []string{"plan:docs/superpowers/plans/x.md#Task 3"},
			Hidden:          []string{"internal/calibration/state_test.go"},
			Features:        map[string]string{"scope": "1-3", "output": "code"},
		}},
		Evidence: map[string][]EvidenceRow{
			"simple_impl": {
				{Agent: "codex", Family: "sol", Effort: "high", Tasks: 3, TasksPassed: 2, Date: "2026-11-05", DuologVersion: "0.9.0"},
				{Agent: "claude", Family: "sonnet", Effort: "medium", Tasks: 3, TasksPassed: 3, Judged: 2, JudgedQuality: quality(2.5), Date: "2026-11-05", DuologVersion: "0.9.0"},
				{Agent: "claude", Family: "opus", Effort: "high", Tasks: 3, TasksPassed: 3, Date: "2026-11-05", DuologVersion: "0.9.0"},
			},
			"docs": {
				{Agent: "claude", Family: "sonnet", Effort: "low", Tasks: 3, TasksPassed: 3, Date: "2026-11-05", DuologVersion: "0.9.0"},
			},
		},
	}
}
