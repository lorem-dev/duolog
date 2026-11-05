package main

import (
	"time"

	"github.com/lorem-dev/duolog/internal/peer"
	"github.com/lorem-dev/duolog/internal/telemetry"
)

// observePeer writes each peer open, send and close as a peer event: the op,
// the agent, the outcome and whole seconds. The peer package only reports
// them; this command writes them. An append that fails is counted in the
// store's health and otherwise ignored, so it cannot change the operation.
func observePeer(s *telemetry.Store) peer.OperationObserver {
	if s == nil {
		return nil
	}
	return func(o peer.Operation) {
		_ = s.Append(telemetry.Peer{
			Op:      o.Op,
			Agent:   o.Agent,
			Secs:    min(max(int64(o.Duration.Round(time.Second)/time.Second), 0), maxSecs),
			Outcome: o.Outcome,
		})
	}
}
