package peer

import (
	"context"
	"time"

	"github.com/lorem-dev/duolog/internal/session"
)

// notifyStuck claims a reminder under the registry lock before sending it.
// Separate wait processes cannot notify twice for the same question.
func (e *env) notifyStuck(ctx context.Context, entry session.Entry, now time.Time) error {
	if e.cfg.Rex.StuckNotifyMinutes <= 0 || ctx.Err() != nil {
		return nil //nolint:nilerr // a cancelled wait sends no reminder, and that is not an error
	}
	var message string
	_, err := e.reg.Update(entry.ID, func(x *session.Entry) {
		if x.Closed || x.Asked == "" || x.QuestionSince.IsZero() || x.QuestionNotified || now.Sub(x.QuestionSince) < time.Duration(e.cfg.Rex.StuckNotifyMinutes)*time.Minute {
			return
		}
		x.QuestionNotified = true
		message = x.ID + ": " + oneLine(x.Asked)
	})
	if err != nil {
		return err
	}
	if message != "" {
		e.opts.Deps.Notify("duolog", message)
	}
	return nil
}
