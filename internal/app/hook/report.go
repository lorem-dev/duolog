package hook

import (
	"os"
	"path/filepath"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/policy"
)

// peerReportNote is the line a peer's prompt carries: where its final answer
// goes. A lead, a plain worker and a peer ID that could name another path get
// none. The report is the one file a peer may write under .duolog.
func peerReportNote(e Env) string {
	peer := value(environment(e), "DUOLOG_PEER")
	if !worker(e) || !policy.ReportTarget(peer, policy.ReportPath(peer)) {
		return ""
	}
	return "duolog: when the task is done, write your final answer to " + policy.ReportPath(peer) +
		" in your checkout: short, with the result first. Write nothing else under .duolog. Then reply \"done\"."
}

// hasReport reports whether the peer wrote its report: a regular file, not a
// link, at the peer's report path, which lies inside the checkout once every
// link on its way is resolved.
func hasReport(root string, entry session.Entry) bool {
	if !policy.ReportTarget(entry.ID, policy.ReportPath(entry.ID)) {
		return false
	}
	if entry.WorkRoot != "" {
		root = entry.WorkRoot
	}
	path := filepath.Join(root, filepath.FromSlash(policy.ReportPath(entry.ID)))
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		return false
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(base, real)
	return err == nil && filepath.IsLocal(rel)
}
