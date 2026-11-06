package delegate

import (
	"os"
	"path/filepath"
	"strings"
)

// absentFrom lists the notices line does not hold yet.
func absentFrom(line string, notices []string) []string {
	var absent []string
	for _, n := range notices {
		if !strings.Contains(line, n) {
			absent = append(absent, n)
		}
	}
	return absent
}

// joinNotices is what follows a line to carry notices: each leads with "; ",
// except one that comes after a notice ending in a full stop, which leads with
// a space, so that ".;" never appears.
func joinNotices(notices []string) string {
	var b strings.Builder
	for i, n := range notices {
		if i > 0 && strings.HasSuffix(notices[i-1], ".") {
			b.WriteString(" ")
		} else {
			b.WriteString("; ")
		}
		b.WriteString(n)
	}
	return b.String()
}

// shownTask is the placement as the line names it: after a quota end its task
// is a path from the current folder; the placement itself keeps the real path.
func shownTask(p Placement, quotaEnded bool) Placement {
	if quotaEnded {
		p.Task = relativeTask(p.Task)
	}
	return p
}

// relativeTask is path as seen from the current folder, in slash form; it stays
// as it is when there is no such path.
func relativeTask(path string) string {
	if cwd, err := os.Getwd(); err == nil {
		if relative, err := filepath.Rel(cwd, path); err == nil {
			path = relative
		}
	}
	return filepath.ToSlash(path)
}
