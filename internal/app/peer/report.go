package peer

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/policy"
	corescreen "github.com/lorem-dev/duolog/internal/core/screen"
)

const (
	// reportLines and reportRunes bound the start of a report that wait and
	// status print.
	reportLines = 15
	reportRunes = 1500
	// reportRead is the most bytes read from a report: enough for reportRunes.
	reportRead = 8192
	// reportCut ends an excerpt that left out the rest of the report.
	reportCut = "[report cut]"
	// noReport is said in the wait line of a task that ended without a report.
	noReport = "no report written"
)

// reportFile returns the absolute path of the peer's report, or "" when the
// peer has none to show. The peer ID must be a plain name, and the file a
// regular file, not a link, that lies inside the checkout the peer works in
// once every link on its way is resolved: a peer must not make duolog print a
// file from anywhere else.
func (e *env) reportFile(entry session.Entry) string {
	path := e.reportTarget(entry)
	if path == "" {
		return ""
	}
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	root := entry.WorkRoot
	if root == "" {
		root = e.root
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return ""
	}
	if rel, err := filepath.Rel(base, real); err != nil || !filepath.IsLocal(rel) {
		return ""
	}
	return path
}

// reportTarget is where the peer writes its report, whether it exists or not,
// and "" for a peer ID that could name another path.
func (e *env) reportTarget(entry session.Entry) string {
	if !policy.ReportTarget(entry.ID, policy.ReportPath(entry.ID)) {
		return ""
	}
	root := entry.WorkRoot
	if root == "" {
		root = e.root
	}
	return filepath.Join(root, filepath.FromSlash(policy.ReportPath(entry.ID)))
}

// reportExcerpt returns the start of the peer's report, or "" when there is
// none.
func (e *env) reportExcerpt(entry session.Entry) string {
	path := e.reportFile(entry)
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, reportRead))
	if err != nil {
		return ""
	}
	return excerpt(string(data))
}

// excerpt is the first reportLines lines and reportRunes characters of a
// report, without control characters other than line breaks, so that a report
// cannot drive the lead's terminal. An excerpt that left something out ends in
// a marker.
func excerpt(text string) string {
	text = strings.ToValidUTF8(text, "?")
	text = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, text)
	lines := strings.Split(strings.TrimSpace(text), "\n")
	cut := false
	if len(lines) > reportLines {
		lines, cut = lines[:reportLines], true
	}
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	out := strings.Join(lines, "\n")
	if runes := []rune(out); len(runes) > reportRunes {
		out, cut = string(runes[:reportRunes]), true
	}
	out = strings.TrimSpace(out)
	if out != "" && cut {
		out += "\n" + reportCut
	}
	return out
}

// freshTaskLine is the line typed into the pane for a task, after the report
// of the task before is gone.
func (e *env) freshTaskLine(entry session.Entry, path string) string {
	e.dropReport(entry)
	report := ""
	if policy.ReportTarget(entry.ID, policy.ReportPath(entry.ID)) {
		report = policy.ReportPath(entry.ID)
	}
	return corescreen.TaskLine(e.peerRoot(entry), path, report)
}

// dropReport removes the report an earlier task left, so that a report found
// after the next task is that task's. It touches only the peer's own report
// path.
func (e *env) dropReport(entry session.Entry) {
	if path := e.reportTarget(entry); path != "" {
		_ = os.Remove(path)
	}
}
