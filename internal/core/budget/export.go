package budget

import "github.com/lorem-dev/duolog/internal/core/config"

// The names below cross the package edge: internal/calibration's run, judge
// and mining code used them while they were package-private. Each one only
// forwards to the private name.

// ResetSlack absorbs a reset time that moves by seconds between readings.
const ResetSlack = resetSlack

// JudgeTokensHigh is the high token estimate of one judge call or rewrite.
const JudgeTokensHigh = judgeTokensHigh

// SpecClasses are the classes a commit cannot test.
var SpecClasses = specClasses

func AccountWindow(ws []Window, kind string) *Window { return accountWindow(ws, kind) }

func ForModel(ws []Window, family string) []Window { return forModel(ws, family) }

func EntryAgent(key string) string { return entryAgent(key) }

func Number(v float64) string { return number(v) }

func CellUSD(cfg *config.Config, agent, model string, tokens int64) float64 {
	return cellUSD(cfg, agent, model, tokens)
}

func ClassTokens(m Manifest, class string) int64 { return classTokens(m, class) }

func HashOf(v any) string { return hashOf(v) }

func TestFile(name string) bool { return testFile(name) }

func Vendored(name string) bool { return vendored(name) }

func GeneratedName(name string) bool { return generatedName(name) }

func ScopeOf(c Candidate) string { return scopeOf(c) }

func OverCap(e Estimate, cfg config.Calibration) bool { return overCap(e, cfg) }

func SpecSource(source string) bool { return specSource(source) }

// Usable is the share of each window the budget's account may use.
func (b BillingBudget) Usable() float64 { return b.usable() }

// Paid reports a route billed per token.
func (b BillingBudget) Paid() bool { return b.paid() }

// Opted reports both opt-ins a paid calibration step needs.
func (b BillingBudget) Opted() bool { return b.opted() }

// Remaining is the USD a paid route may still spend.
func (b BillingBudget) Remaining() float64 { return b.remaining() }

// OutstandingFor is the sum of the run's charges on w's window that the
// readings do not show yet.
func (s Spend) OutstandingFor(agent string, w Window) float64 { return s.outstanding(agent, w) }

// JudgeTokensLow is the low token estimate of one judge call or rewrite.
const JudgeTokensLow = judgeTokensLow

// AddJudge counts weekly points of the agent's spend as judging's.
func (s *Spend) AddJudge(agent string, pct float64) {
	if s.JudgePct == nil {
		s.JudgePct = map[string]float64{}
	}
	s.JudgePct[agent] += pct
}
