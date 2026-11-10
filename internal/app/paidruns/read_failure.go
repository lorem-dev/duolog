package paidruns

import (
	"errors"

	"github.com/lorem-dev/duolog/internal/quota"
)

// readFailureDetail names a failed read without quoting arbitrary reader
// errors, which may contain credentials or route values. Only known fixed
// messages get a specific explanation; this changes no admission decision.
func readFailureDetail(err error) string {
	if err == nil {
		return ""
	}
	reason := "quota read failed"
	switch {
	case errors.Is(err, quota.ErrTimedOut):
		reason = "quota read timed out or was canceled"
	case err.Error() == "no quota windows":
		reason = "quota read returned no supported windows"
	}
	return reason + "; subscription billing could not be verified"
}
