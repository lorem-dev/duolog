package gate

import "github.com/lorem-dev/duolog/internal/core/policy"

// The tests of this package were written when the gate's decisions lived here.
// They reach those names through these test-only aliases, so the test bodies
// stay as they were.

type (
	Action    = policy.Action
	Decision  = policy.Decision
	LeadRules = policy.LeadRules
)

const (
	Allow = policy.Allow
	Deny  = policy.Deny
	Ask   = policy.Ask

	RuleOutside     = policy.RuleOutside
	RuleAgentLaunch = policy.RuleAgentLaunch
	RuleProtected   = policy.RuleProtected
	RuleState       = policy.RuleState
	RuleReport      = policy.RuleReport
)

var (
	Normalize       = policy.Normalize
	NormalizeAll    = policy.NormalizeAll
	Render          = policy.Render
	RenderLead      = policy.RenderLead
	PatchPaths      = policy.PatchPaths
	MCPTarget       = policy.MCPTarget
	MCPName         = policy.MCPName
	ExecOutside     = policy.ExecOutside
	PersonOnly      = policy.PersonOnly
	Evaluate        = policy.Evaluate
	ShellParts      = policy.ShellParts
	RelativeTarget  = policy.RelativeTarget
	InClaudeTemp    = policy.InClaudeTemp
	RememberPattern = policy.RememberPattern
	ProgramName     = policy.ProgramName
	ReportPath      = policy.ReportPath
	ReportTarget    = policy.ReportTarget
	StateRead       = policy.StateRead
	WorkerMode      = policy.WorkerMode
)
