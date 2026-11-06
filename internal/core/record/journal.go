package record

import "time"

type FileChange struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

type Delegation struct {
	Time             time.Time         `json:"time"`
	ID               string            `json:"id"`
	Task             string            `json:"task"`
	Step             int               `json:"step"`
	Lead             string            `json:"lead"`
	Role             string            `json:"role"`
	Features         map[string]string `json:"features,omitempty"`
	StatedClass      string            `json:"stated_class,omitempty"`
	RuleClass        string            `json:"rule_class,omitempty"`
	Class            string            `json:"class,omitempty"`
	Ladder           string            `json:"ladder,omitempty"`
	Entry            string            `json:"entry,omitempty"`
	RequestedEntry   string            `json:"requested_entry,omitempty"`
	Why              string            `json:"why,omitempty"`
	RoleMismatch     bool              `json:"role_mismatch,omitempty"`
	Agent            string            `json:"agent"`
	Model            string            `json:"model"`
	Effort           string            `json:"effort,omitempty"`
	EffortCapped     bool              `json:"effort_capped,omitempty"`
	Input            int64             `json:"input"`
	Cached           int64             `json:"cached"`
	CacheWrite       int64             `json:"cache_write,omitempty"`
	CacheWriteHour   int64             `json:"cache_write_1h,omitempty"`
	Output           int64             `json:"output"`
	USD              float64           `json:"usd"`
	Priced           bool              `json:"priced"`
	Seconds          float64           `json:"seconds"`
	Done             bool              `json:"done"`
	RunHere          bool              `json:"run_here,omitempty"`
	Interrupted      bool              `json:"interrupted,omitempty"`
	Needs            []string          `json:"needs,omitempty"`
	GateDenials      int               `json:"gate_denials,omitempty"`
	GateAsks         int               `json:"gate_asks,omitempty"`
	EscalatedFrom    string            `json:"escalated_from,omitempty"`
	EscalationReason string            `json:"escalation_reason,omitempty"`
	ProfileOf        string            `json:"profile_of,omitempty"`
	Outcome          string            `json:"outcome,omitempty"` // "", OutcomeInfra or OutcomeLead
	Base             string            `json:"base,omitempty"`
	BeforeTree       string            `json:"before_tree,omitempty"`
	AfterTree        string            `json:"after_tree,omitempty"`
	Files            []FileChange      `json:"files,omitempty"`
}

// A delegation with one of these outcomes says nothing about the quality of
// its entry, so aggregation and escalation skip it.
const (
	// OutcomeInfra: the run ended on a quota or rate limit.
	OutcomeInfra = "infra"
	// OutcomeLead: control returned to the lead without a quality result.
	OutcomeLead = "lead"
)

// QualityRun excludes returns to the lead, interruptions and questions. A real
// worker failure remains eligible; only that failure says something on quality.
func (d Delegation) QualityRun() bool {
	return d.Outcome != OutcomeInfra && d.Outcome != OutcomeLead && !d.RunHere && !d.Interrupted && len(d.Needs) == 0
}

type Task struct {
	Kind        string    `json:"kind"` // "finish" or "outcome"
	Time        time.Time `json:"time"`
	ID          string    `json:"id"`
	Lead        string    `json:"lead,omitempty"`
	Title       string    `json:"title,omitempty"`
	Base        string    `json:"base,omitempty"`
	Final       string    `json:"final,omitempty"`
	FinalTree   string    `json:"final_tree,omitempty"`
	Delegations []string  `json:"delegations,omitempty"`
	USD         float64   `json:"usd,omitempty"`
	Outcome     string    `json:"outcome,omitempty"`
	Note        string    `json:"note,omitempty"`
}

type Check struct {
	Command string  `json:"command"`
	Exit    int     `json:"exit"`
	Seconds float64 `json:"seconds"`
}

type Score struct {
	Criterion     string `json:"criterion"`
	Original      int    `json:"original"`
	Profile       int    `json:"profile"`
	OriginalCount *int   `json:"original_count,omitempty"`
	ProfileCount  *int   `json:"profile_count,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type Verdict struct {
	Kind           string    `json:"kind"` // "verdict" or "decision"
	Time           time.Time `json:"time"`
	ID             string    `json:"id"`
	Task           string    `json:"task,omitempty"`
	Delegation     string    `json:"delegation,omitempty"`
	Class          string    `json:"class,omitempty"`
	Ladder         string    `json:"ladder,omitempty"`
	Original       string    `json:"original,omitempty"`
	Profile        string    `json:"profile,omitempty"`
	Judge          string    `json:"judge,omitempty"`
	OriginalLabel  string    `json:"original_label,omitempty"`
	Scores         []Score   `json:"scores,omitempty"`
	OriginalChecks []Check   `json:"original_checks,omitempty"`
	ProfileChecks  []Check   `json:"profile_checks,omitempty"`
	ReplayUSD      float64   `json:"replay_usd,omitempty"`
	JudgeUSD       float64   `json:"judge_usd,omitempty"`
	Decision       string    `json:"decision,omitempty"`
	Note           string    `json:"note,omitempty"`
	Source         string    `json:"source,omitempty"` // "calibration"
	Run            string    `json:"run,omitempty"`
	Verification   string    `json:"verification,omitempty"` // hidden, check, judge_only
	Disputed       bool      `json:"disputed,omitempty"`
}

// CalibrationResult is one entry's check result on one calibration task, or,
// with Kind "drop", a task the person dropped from its run.
type CalibrationResult struct {
	Kind         string    `json:"kind"` // "result" or "drop"
	Time         time.Time `json:"time"`
	Run          string    `json:"run"`
	Task         string    `json:"task"`
	Class        string    `json:"class,omitempty"`
	Ladder       string    `json:"ladder,omitempty"`
	Entry        string    `json:"entry,omitempty"`
	Verification string    `json:"verification,omitempty"`
	Passed       bool      `json:"passed"`
	Source       string    `json:"source"` // "calibration"
}

type OpenTask struct {
	ID      string    `json:"id"`
	Lead    string    `json:"lead"`
	Title   string    `json:"title,omitempty"`
	Base    string    `json:"base,omitempty"`
	Started time.Time `json:"started"`
}

// Declined records a class proposal the person declined.
type Declined struct {
	Time time.Time `json:"time"`
	Name string    `json:"name"`
}
