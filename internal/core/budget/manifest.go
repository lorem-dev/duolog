package budget

import (
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/learn"
	"github.com/lorem-dev/duolog/internal/core/record"
)

// Candidate is one past change that may become a calibration task.
type Candidate struct {
	ID              string // "k-" + first 12 hex of sha256(sources)
	Parent, Ref     string // parent of the first commit; last commit
	Commits         []string
	Time            time.Time
	StatementSource string // "plan", "spec", "pr", "issue", "commit", "reconstructed"
	Statement       string
	NeedsRewrite    bool // "commit" and "reconstructed"
	Sources         []string
	Subject         string              // first message line; never shown to a candidate
	Files           []record.FileChange // generated and vendored dropped
	Lines           int
	Components      []string
	Hidden          []string // test files the change adds or changes
	HiddenNames     []string
	Role            string
	Features        map[string]string
	Class           string
	Large           bool // inside the large stratum
	Excluded        string

	// Facts Filter reads; they never leave the package, so no author name
	// or message reaches a manifest.
	Body       string
	Author     string
	Formatting bool
	Generated  map[string]bool
	Dropped    int
}

type ManifestTask struct {
	ID              string            `yaml:"id" json:"id"`
	Statement       string            `yaml:"statement" json:"statement"`
	StatementSource string            `yaml:"statement_source" json:"statement_source"`
	Class           string            `yaml:"class" json:"class"`
	Verification    string            `yaml:"verification" json:"verification"`
	Sources         []string          `yaml:"sources,omitempty" json:"sources,omitempty"` // "commit:<sha>", "plan:<path>#Task 3", "pr:12", "adr:<path>"
	Hidden          []string          `yaml:"hidden,omitempty" json:"hidden,omitempty"`   // hidden test files
	Features        map[string]string `yaml:"features,omitempty" json:"features,omitempty"`
}

// ClassCount says what mining found for one class.
type ClassCount struct {
	Candidates int    `json:"candidates"`
	Valid      int    `json:"valid"`
	Selected   int    `json:"selected"`
	Reserve    int    `json:"reserve"`
	WindowDays int    `json:"window_days"`
	Skipped    string `json:"skipped,omitempty"`
}

type Exclusion struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// Manifest is the sample of one calibration run, kept in its manifest.json.
type Manifest struct {
	Seed         int64                 `json:"seed"`
	Tasks        []ManifestTask        `json:"tasks"`   // selected, in sample order; Round 1 first
	Round2       []string              `json:"round2"`  // task IDs held for round 2
	Reserve      map[string][]string   `json:"reserve"` // class -> task IDs
	Counts       map[string]ClassCount `json:"counts"`
	Excluded     []Exclusion           `json:"excluded"`
	Estimates    map[string]Estimate   `json:"estimates"`    // selected and reserve tasks
	Verification map[string]string     `json:"verification"` // task ID -> verification, selected and reserve
	Rewrite      []string              `json:"rewrite"`      // task IDs whose statement is rewritten in the run
	RepoFiles    int                   `json:"repo_files"`
	HistoryDays  int                   `json:"history_days"`
	CheckSeconds float64               `json:"check_seconds"`
}

// RecentDays is the age in days up to which a task counts as recent.
const RecentDays = 90

// JudgeTargets gives a pair of different agents the first judge of each, and
// a pair of one agent the first judge of another agent. Without such a judge
// it falls back to learn.JudgeTarget.
func JudgeTargets(l config.Learning, a, b config.Target) []config.Target {
	var out []config.Target
	seen := map[string]bool{}
	for _, e := range l.Judges {
		switch {
		case a.Agent == b.Agent && e.Agent != a.Agent:
			return []config.Target{e.Target()}
		case a.Agent != b.Agent && (e.Agent == a.Agent || e.Agent == b.Agent) && !seen[e.Agent]:
			seen[e.Agent] = true
			out = append(out, e.Target())
		}
	}
	if len(out) == 0 {
		if t := learn.JudgeTarget(l, a, b); t.Agent != "" {
			out = append(out, t)
		}
	}
	return out
}

// TargetKey names a target as agent/model/effort.
func TargetKey(t config.Target) string { return t.Agent + "/" + t.Model + "/" + t.Effort }
