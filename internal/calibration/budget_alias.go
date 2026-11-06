package calibration

import "github.com/lorem-dev/duolog/internal/core/budget"

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

var (
	Admit         = budget.Admit
	AdmitBilled   = budget.AdmitBilled
	BuildPlan     = budget.BuildPlan
	Cells         = budget.Cells
	Classify      = budget.Classify
	CostOf        = budget.CostOf
	EstimateTask  = budget.EstimateTask
	Filter        = budget.Filter
	JournalMeans  = budget.JournalMeans
	JudgeCost     = budget.JudgeCost
	JudgeTargets  = budget.JudgeTargets
	LimitPaidPlan = budget.LimitPaidPlan
	RenderPlan    = budget.RenderPlan
)

// Names that were private to this package before the move; the code here
// keeps its spelling.
const (
	resetSlack      = budget.ResetSlack
	judgeTokensHigh = budget.JudgeTokensHigh
	judgeTokensLow  = budget.JudgeTokensLow
)

var (
	specClasses   = budget.SpecClasses
	accountWindow = budget.AccountWindow
	forModel      = budget.ForModel
	entryAgent    = budget.EntryAgent
	number        = budget.Number
	cellUSD       = budget.CellUSD
	classTokens   = budget.ClassTokens
	hashOf        = budget.HashOf
	testFile      = budget.TestFile
	vendored      = budget.Vendored
	generatedName = budget.GeneratedName
	scopeOf       = budget.ScopeOf
	overCap       = budget.OverCap
	specSource    = budget.SpecSource
	targetKey     = budget.TargetKey
)
