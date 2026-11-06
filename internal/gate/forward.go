package gate

import corepolicy "github.com/lorem-dev/duolog/internal/core/policy"

// The gate's decisions live in core/policy. These names keep the old
// import path working until the importers switch (Task 20). The import is
// aliased because the package's tests have a helper named policy.

type (
	Action    = corepolicy.Action
	Decision  = corepolicy.Decision
	LeadRules = corepolicy.LeadRules
)

const (
	Allow = corepolicy.Allow
	Deny  = corepolicy.Deny
	Ask   = corepolicy.Ask

	RuleOutside     = corepolicy.RuleOutside
	RuleAgentLaunch = corepolicy.RuleAgentLaunch
	RuleProtected   = corepolicy.RuleProtected
	RuleState       = corepolicy.RuleState
	RuleReport      = corepolicy.RuleReport
)

// validateRule keeps the lead-rule import calling the policy's own check.
var validateRule = corepolicy.ValidateRule

var (
	Normalize       = corepolicy.Normalize
	NormalizeAll    = corepolicy.NormalizeAll
	Render          = corepolicy.Render
	RenderLead      = corepolicy.RenderLead
	PatchPaths      = corepolicy.PatchPaths
	MCPTarget       = corepolicy.MCPTarget
	MCPName         = corepolicy.MCPName
	ExecOutside     = corepolicy.ExecOutside
	PersonOnly      = corepolicy.PersonOnly
	Evaluate        = corepolicy.Evaluate
	ShellParts      = corepolicy.ShellParts
	RelativeTarget  = corepolicy.RelativeTarget
	InClaudeTemp    = corepolicy.InClaudeTemp
	RememberPattern = corepolicy.RememberPattern
	ProgramName     = corepolicy.ProgramName
	ReportPath      = corepolicy.ReportPath
	ReportTarget    = corepolicy.ReportTarget
	StateRead       = corepolicy.StateRead
	WorkerMode      = corepolicy.WorkerMode
)
