package hook

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/lorem-dev/duolog/internal/adapter/session"
)

// hintStep is how many further tokens the fresh-session reminder waits for
// before it comes again.
const hintStep = 100000

// freshHint is the reminder that a Claude lead whose context has grown large
// may offer the person a fresh lead session, or "". It is said while no peer
// or foreground worker of the lead runs, once, and again each further
// hintStep tokens; the size it was last said at is kept in the lead's shown
// record. Only a Claude transcript carries the size, so a lead whose size
// cannot be read, such as a Codex lead, gets none.
// A failure or a panic is no line: a hook never fails over it.
func freshHint(ctx context.Context, e Env, s *state) (line string) {
	defer func() {
		if recover() != nil {
			line = ""
		}
	}()
	if s == nil || peerOrWorker(e) || s.cfg.Lead.FreshSessionHint <= 0 {
		return ""
	}
	tokens, ok := session.ClaudeContextTokens(s.lead.TranscriptPath)
	if !ok || tokens < s.cfg.Lead.FreshSessionHint || !leadIdle(ctx, e, s) {
		return ""
	}
	said := false
	withShown(filepath.Join(e.Root, ".duolog"), s.lead.ID, func(seen *shownLine) bool {
		if seen.Hint > 0 && tokens < seen.Hint+hintStep {
			return false
		}
		seen.Hint = tokens
		said = true
		return true
	})
	if !said {
		return ""
	}
	return fmt.Sprintf("Context is about %dk tokens. When this wave of cards is finished and written in the plan, "+
		"offer the person a fresh lead session, with the plan file and the next card as the hand-over. "+
		"Do not start it or close this session without their answer.", tokens/1000)
}

// leadIdle reports that no peer and no foreground worker of the lead runs.
func leadIdle(ctx context.Context, e Env, s *state) bool {
	if waiting, err := s.reg.IsWaiting(s.lead.ID); err != nil || waiting {
		return false
	}
	if e.Load == nil {
		return true
	}
	for _, a := range e.Load(ctx, s.lead.ID).Agents {
		if a.Running > 0 {
			return false
		}
	}
	return true
}
