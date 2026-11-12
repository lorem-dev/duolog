package account

import "errors"

// ErrStateBusy means another duolog process holds the account state lock.
// It says nothing about the account's login, quota or billing route.
var ErrStateBusy = errors.New("another duolog process holds the account state")

// TransientError is the error of a read that failed and found nothing about
// the account or its route: it ran out of time, or the agent's CLI ended
// without a report and the login it could read still names a subscription.
// Trying again may well succeed. A read that saw any evidence, such as a
// report that refuses windows or a login on an API key, is never transient.
type TransientError string

func (e TransientError) Error() string { return string(e) }

// IsTransient reports whether err is, or wraps, a TransientError.
func IsTransient(err error) bool {
	var transient TransientError
	return errors.As(err, &transient)
}
