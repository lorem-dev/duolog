package gate

import (
	"path/filepath"
	"strings"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/pathutil"
)

// OwnToolResults says whether target, an absolute path, is the saved tool
// results folder of the Claude session sessionID, or a path inside it. Claude
// saves a large tool output at <claudeDir>/projects/<project>/<session>/tool-results
// and tells the worker to read it. Nothing else under claudeDir qualifies: not
// another session's folder, not the session's other files, not settings.
func OwnToolResults(claudeDir, sessionID, target string) bool {
	if claudeDir == "" || !session.ValidSessionID(sessionID) || !filepath.IsAbs(target) {
		return false
	}
	base := pathutil.Resolved(filepath.Join(claudeDir, "projects"))
	rel, err := filepath.Rel(base, pathutil.Resolved(filepath.Clean(target)))
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 3 || parts[0] == "" || parts[0] == ".." || parts[0] == "." {
		return false
	}
	for _, p := range parts {
		if p == ".." {
			return false
		}
	}
	return parts[1] == sessionID && parts[2] == "tool-results"
}
