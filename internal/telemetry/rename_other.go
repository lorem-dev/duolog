//go:build !windows && !linux && !darwin

package telemetry

import (
	"errors"
	"os"
)

// renameNoReplace has no rename that refuses to replace here; newStamp falls
// back to a link.
func renameNoReplace(*os.Root, string, string) error {
	return unsupported(errors.ErrUnsupported)
}

// renameFailure leaves every error as it is: renameNoReplace marks its own.
func renameFailure(err error) error { return err }
