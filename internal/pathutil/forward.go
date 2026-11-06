// Package pathutil forwards to internal/core/pathutil for the security
// boundary, whose files keep their imports until they move.
package pathutil

import corepathutil "github.com/lorem-dev/duolog/internal/core/pathutil"

var (
	Windows  = corepathutil.Windows
	Slashes  = corepathutil.Slashes
	Clean    = corepathutil.Clean
	IsAbs    = corepathutil.IsAbs
	Outside  = corepathutil.Outside
	Relative = corepathutil.Relative
	Native   = corepathutil.Native
	Resolved = corepathutil.Resolved
)
