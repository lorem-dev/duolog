//go:build !windows && !linux && !darwin

package telemetry

import (
	"errors"
	"os"
)

// renameNoReplace has no rename that refuses to replace here; newStamp falls
// back to a link.
func renameNoReplace(*os.Root, string, string) error { return errors.ErrUnsupported }
