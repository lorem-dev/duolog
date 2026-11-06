package runner

import "github.com/lorem-dev/duolog/internal/core/run"

// The launch spec types live in core/run; these names keep the importers of
// runner as they are. Result stays here while its Billing field is a
// quota.RunEvidence.
type (
	Spec  = run.Spec
	MCP   = run.MCP
	Usage = run.Usage
)

const (
	CodeNone    = run.CodeNone
	CodeQuota   = run.CodeQuota
	CodeAuth    = run.CodeAuth
	CodeTimeout = run.CodeTimeout
	CodeExit    = run.CodeExit
	CodeParse   = run.CodeParse
	CodeGate    = run.CodeGate
)

// ErrTurnCap is Run's error for a worker that reached Spec.MaxTurns.
var ErrTurnCap = run.ErrTurnCap
