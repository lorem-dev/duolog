package hook

import (
	"context"
	"path/filepath"
	"time"

	"github.com/lorem-dev/duolog/internal/app/load"
)

// loadLine is the lead's load line, or "" when there is none to show: the
// session is no registered lead, no Load is set, nothing is running or
// limited, or the line fails to compute. A hook never fails over it.
//
// A prompt always shows the line. A bus delivery (quiet) shows it only when it
// is due: a number changed, or 5 minutes passed since the lead last saw it.
func loadLine(ctx context.Context, e Env, s *state, quiet bool) (line string) {
	// A peer pane carries DUOLOG_PEER; a Codex peer would otherwise register
	// as a lead at its first prompt.
	if s == nil || e.Load == nil || worker(e) || value(environment(e), "DUOLOG_PEER") != "" {
		return ""
	}
	defer func() {
		if recover() != nil {
			line = ""
		}
	}()
	dir := filepath.Join(e.Root, ".duolog")
	line = e.Load(ctx, s.lead.ID).Line(load.QueueEmpty(dir))
	if line == "" {
		return ""
	}
	now := time.Now()
	if quiet && !load.Due(dir, s.lead.ID, line, now) {
		return ""
	}
	if !quiet {
		load.Mark(dir, s.lead.ID, line, now)
	}
	return line
}
