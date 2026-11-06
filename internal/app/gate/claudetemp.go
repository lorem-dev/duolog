package gate

import (
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
)

// claudeProjectChars are the characters Claude replaces with - when it names a
// project folder after the folder a session started in.
var claudeProjectChars = regexp.MustCompile(`[^A-Za-z0-9]`)

// ClaudeTemp names the temp folder of one Claude session on a POSIX host:
// <tmp>/claude-<uid>/<project>/<session>, where <project> is folder as Claude
// names it. Claude keeps the session's scratchpad and task output there. tmp
// is resolved, so /tmp and /private/tmp compare as one; the folders below it
// are not, so a symlink among them leads nowhere InClaudeTemp accepts. It
// returns "" on Windows and for a session id that is not one.
func ClaudeTemp(tmp string, uid int, sessionID, folder string) string {
	if runtime.GOOS == "windows" || tmp == "" || folder == "" || !session.ValidSessionID(sessionID) {
		return ""
	}
	return filepath.Join(pathutil.Resolved(tmp), "claude-"+strconv.Itoa(uid), claudeProjectChars.ReplaceAllString(folder, "-"), sessionID)
}
