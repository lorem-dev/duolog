package hook

import (
	"context"
	"path/filepath"
	"time"

	"github.com/lorem-dev/duolog/internal/app/load"
)

// peerOrWorker reports a session in a peer's pane or a worker. A Codex peer
// would otherwise register as a lead at its first prompt, so DUOLOG_PEER
// counts as well as the depth.
func peerOrWorker(e Env) bool {
	return worker(e) || value(environment(e), "DUOLOG_PEER") != ""
}

// loadLine is the lead's load line, or "" when there is none to show: the
// session is no registered lead, no Load is set, nothing is running or
// limited, or the line fails to compute. A hook never fails over it.
//
// A prompt always shows the line. A bus delivery (quiet) shows it only when it
// is due: a number changed, or 5 minutes passed since the lead last saw it.
func loadLine(ctx context.Context, e Env, s *state, quiet bool) (line string) {
	if s == nil || e.Load == nil || peerOrWorker(e) {
		return ""
	}
	defer func() {
		if recover() != nil {
			line = ""
		}
	}()
	dir := filepath.Join(e.Root, ".duolog")
	line = e.Load(ctx, s.lead.ID).Line()
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

// startQueue lets a registered lead's prompt start its queue run, which opens
// queued peers while sessions are free. A peer or worker starts none.
func startQueue(ctx context.Context, e Env, s *state) {
	if s == nil || e.QueueRun == nil || peerOrWorker(e) {
		return
	}
	e.QueueRun(ctx, s.lead.ID)
}

// followIdle calls PeerIdle once for each peer whose peer-idle message the
// lead was just given, so a peer that finished its turn lets queued work
// start.
func followIdle(ctx context.Context, e Env, s *state, idle []string) {
	if s == nil || e.PeerIdle == nil || peerOrWorker(e) {
		return
	}
	for _, id := range idle {
		e.PeerIdle(ctx, s.lead.ID, id)
	}
}
