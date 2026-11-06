package budget

import "time"

type CellState string

const (
	CellPending     CellState = "pending"
	CellRunning     CellState = "running"
	CellDone        CellState = "done"
	CellFailedLimit CellState = "failed_limit"
	CellInvalid     CellState = "invalid"
	CellSkipped     CellState = "skipped"
	CellLeakage     CellState = "leakage_exposed"
)

// CellSetupChanged is a cell whose candidate changed how a check is defined:
// its checks ran but are no pass or failure; the person decides in judge.
const CellSetupChanged CellState = "check_setup_changed"

// Cell is one task replayed on one entry.
type Cell struct {
	ID    string    `json:"id"`
	Task  string    `json:"task"`
	Class string    `json:"class"`
	Entry string    `json:"entry"`
	Round int       `json:"round"` // 1 or 2
	State CellState `json:"state"`
	// Passed is true when the hidden tests and the class checks all exit 0.
	Passed  bool    `json:"passed,omitempty"`
	Tokens  int64   `json:"tokens,omitempty"`
	USD     float64 `json:"usd,omitempty"`
	Seconds float64 `json:"seconds,omitempty"`
	// Weekly and Window are measured percentage points, 0 when unmeasured.
	Weekly float64 `json:"weekly,omitempty"`
	Window float64 `json:"window,omitempty"`
	Reason string  `json:"reason,omitempty"` // "time cap", "raced out", "provider limit", ...
	Tree   string  `json:"tree,omitempty"`   // temp tree while running
	// SetupChanged are the files of a check_setup_changed cell that define
	// how a check runs and that its candidate changed.
	SetupChanged []string `json:"setup_changed,omitempty"`
}

type WindowSpend struct {
	ResetsAt  time.Time `json:"resets_at"`
	StartUsed float64   `json:"start_used"`
	LastUsed  float64   `json:"last_used"`
}

type Spend struct {
	WeeklyPct map[string]float64     `json:"weekly_pct,omitempty"` // agent -> percentage points spent by this run
	Windows   map[string]WindowSpend `json:"windows,omitempty"`    // agent -> current five-hour window
	// Levels holds each window's last reading by agent/kind[/model], and
	// Outstanding, by the same key, the run's charges that reading may not
	// show yet. The lead reserve is kept against the two together.
	Levels      map[string]WindowSpend `json:"levels,omitempty"`
	Outstanding map[string]Outstanding `json:"outstanding,omitempty"`
	// JudgePct is the part of WeeklyPct that spends the plan's judge share:
	// judge calls and the estimates of planned rewrites.
	JudgePct map[string]float64 `json:"judge_pct,omitempty"`
	// PaidUSD is what the run's steps on a paid route cost, by agent, and
	// JudgeUSD the part of it judge calls and planned rewrites took; the plan's
	// approved USD bounds them.
	PaidUSD  map[string]float64 `json:"paid_usd,omitempty"`
	JudgeUSD map[string]float64 `json:"judge_usd,omitempty"`
}

// Ran reports whether the cell ran to an end: its spend is in the run's,
// and a new approval keeps it as it is.
func (c Cell) Ran() bool {
	return c.State == CellDone || c.State == CellFailedLimit || c.State == CellLeakage || c.State == CellSetupChanged
}
