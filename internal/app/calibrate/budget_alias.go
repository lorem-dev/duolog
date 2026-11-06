package calibration

import (
	"io"
	"time"

	"github.com/lorem-dev/duolog/internal/core/budget"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/record"
)

// Aliases for the names that moved to internal/core/budget, so the callers of
// this package do not change.

const (
	ReserveWeekly = budget.ReserveWeekly
	ReserveWindow = budget.ReserveWindow
	BoundWeekly   = budget.BoundWeekly
	BoundWindow   = budget.BoundWindow
	QuotaCacheTTL = budget.QuotaCacheTTL
	KindWeekly    = budget.KindWeekly
	KindFiveHour  = budget.KindFiveHour

	VerdictSubscription = budget.VerdictSubscription
	VerdictOverage      = budget.VerdictOverage
	VerdictPaid         = budget.VerdictPaid

	CellPending      = budget.CellPending
	CellRunning      = budget.CellRunning
	CellDone         = budget.CellDone
	CellFailedLimit  = budget.CellFailedLimit
	CellInvalid      = budget.CellInvalid
	CellSkipped      = budget.CellSkipped
	CellLeakage      = budget.CellLeakage
	CellSetupChanged = budget.CellSetupChanged
)

type (
	BillingBudget = budget.BillingBudget
	Candidate     = budget.Candidate
	Cell          = budget.Cell
	CellCost      = budget.CellCost
	CellState     = budget.CellState
	Charge        = budget.Charge
	ClassCount    = budget.ClassCount
	ClassMeans    = budget.ClassMeans
	ClassPlan     = budget.ClassPlan
	Decision      = budget.Decision
	Estimate      = budget.Estimate
	Exclusion     = budget.Exclusion
	Manifest      = budget.Manifest
	ManifestTask  = budget.ManifestTask
	Mean          = budget.Mean
	Outstanding   = budget.Outstanding
	Plan          = budget.Plan
	PlanEntry     = budget.PlanEntry
	PlanInput     = budget.PlanInput
	QuotaCache    = budget.QuotaCache
	QuotaReader   = budget.QuotaReader
	Rate          = budget.Rate
	Share         = budget.Share
	Spend         = budget.Spend
	Window        = budget.Window
	WindowSpend   = budget.WindowSpend
)

// NextStep names the duolog calibrate command after each step.
var NextStep = budget.NextStep

func Admit(agent string, ws []Window, cost CellCost, spend Spend, cfg config.Calibration, held float64, now time.Time) Decision {
	return budget.Admit(agent, ws, cost, spend, cfg, held, now)
}

func AdmitBilled(agent string, b BillingBudget, ws []Window, cost CellCost, spend Spend, cfg config.Calibration, held float64, now time.Time) Decision {
	return budget.AdmitBilled(agent, b, ws, cost, spend, cfg, held, now)
}

func BuildPlan(in PlanInput) (Plan, error) { return budget.BuildPlan(in) }

func Cells(p Plan, seed int64) []Cell { return budget.Cells(p, seed) }

func Classify(c *Candidate, cfg *config.Config) { budget.Classify(c, cfg) }

func CostOf(rate *Rate, tokens int64, measured []CellCost) CellCost {
	return budget.CostOf(rate, tokens, measured)
}

func EstimateTask(c Candidate, m ClassMeans, checkSeconds float64, cfg config.Calibration) Estimate {
	return budget.EstimateTask(c, m, checkSeconds, cfg)
}

func Filter(c *Candidate, f config.CalibrationFilter, large bool) { budget.Filter(c, f, large) }

func JournalMeans(ds []record.Delegation) ClassMeans { return budget.JournalMeans(ds) }

func JudgeCost(rate *Rate, cellTokens int64) CellCost { return budget.JudgeCost(rate, cellTokens) }

func JudgeTargets(l config.Learning, a, b config.Target) []config.Target {
	return budget.JudgeTargets(l, a, b)
}

func LimitPaidPlan(p Plan, budgets map[string]BillingBudget) (Plan, error) {
	return budget.LimitPaidPlan(p, budgets)
}

func RenderPlan(w io.Writer, p Plan) error { return budget.RenderPlan(w, p) }

// Names that were private to this package before the move; the code here
// keeps its spelling.
const (
	resetSlack      = budget.ResetSlack
	judgeTokensHigh = budget.JudgeTokensHigh
	judgeTokensLow  = budget.JudgeTokensLow
)

var specClasses = budget.SpecClasses

func accountWindow(ws []Window, kind string) *Window { return budget.AccountWindow(ws, kind) }

func forModel(ws []Window, family string) []Window { return budget.ForModel(ws, family) }

func entryAgent(key string) string { return budget.EntryAgent(key) }

func number(v float64) string { return budget.Number(v) }

func cellUSD(cfg *config.Config, agent, model string, tokens int64) float64 {
	return budget.CellUSD(cfg, agent, model, tokens)
}

func classTokens(m Manifest, class string) int64 { return budget.ClassTokens(m, class) }

func hashOf(v any) string { return budget.HashOf(v) }

func testFile(name string) bool { return budget.TestFile(name) }

func vendored(name string) bool { return budget.Vendored(name) }

func generatedName(name string) bool { return budget.GeneratedName(name) }

func scopeOf(c Candidate) string { return budget.ScopeOf(c) }

func overCap(e Estimate, cfg config.Calibration) bool { return budget.OverCap(e, cfg) }

func specSource(source string) bool { return budget.SpecSource(source) }

func targetKey(t config.Target) string { return budget.TargetKey(t) }
