//go:build windows

package workerenv

import "io/fs"

// windowsModes is true where mode bits say nothing about access.
const windowsModes = true

// ownedByUser is true on Windows: a folder under the user's temp folder is
// the user's, and the mode bits do not describe access there.
func ownedByUser(fs.FileInfo) bool { return true }
