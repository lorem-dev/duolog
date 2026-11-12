package delegate

import (
	"errors"
	"fmt"

	"github.com/lorem-dev/duolog/internal/adapter/session"
)

// warn keeps post-run bookkeeping separate from the worker's own result.
func (out *Outcome) warn(step string, err error) {
	if err != nil {
		out.warnings = append(out.warnings, oneLine(step+" accounting failed: "+err.Error()))
	}
}

// quotaEndedUnsettled does what a run that ended on its quota does, for one
// whose settlement failed and so stops at once: the agent is unavailable
// until its reset, and the worker's thread is not resumed.
func (out *Outcome) quotaEndedUnsettled(reg *session.Registry, agent string, entry *session.Entry) {
	if !out.Result.QuotaEnded {
		return
	}
	out.warn("quota", reg.MarkUnavailable(agent, out.Result.ResetAt))
	if entry != nil {
		out.warn("session", reg.Close(entry.ID))
	}
}

func (out *Outcome) finishLearning(learnErr error, started bool, dir string, err *error) {
	if learnErr == nil {
		return
	}
	if !started {
		*err = errors.Join(*err, fmt.Errorf("learning: %w", learnErr))
		return
	}
	out.warn("learning", learnErr)
	out.Line = finalLine(out.Line, out.warnings, dir)
}
