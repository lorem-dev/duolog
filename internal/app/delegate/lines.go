package delegate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
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

// withoutNotice takes the last copy of notice out of line, with the "; " that
// leads it.
func withoutNotice(line, notice string) string {
	i := strings.LastIndex(line, notice)
	if i < 0 {
		return line
	}
	head := strings.TrimSuffix(strings.TrimRight(line[:i], " "), ";")
	return oneLine(head + " " + line[i+len(notice):])
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// maxLine is what the lead reads for one worker. It costs tokens on every turn.
const maxLine = 512

// resultLine gives the summary only the room that needs, files and warnings leave.
// A warning such as "rules are off" or "outside=" must reach the lead, so a line
// that is still too long after the summary is cut gets its files list cut at a
// comma, never a warning. The summary and the files are also in answer.json.
func resultLine(done bool, needs, summary, files, warning, pointer string) string {
	line := func(summary, files string) string {
		return oneLine(fmt.Sprintf("done=%t%s summary=%s files=%s%s", done, needs, summary, files, warning))
	}
	summary = oneLine(summary)
	if over := len(line(summary, files)) - maxLine; over > 0 {
		cut := " ... [cut; " + pointer + "]"
		summary = short(summary, max(0, len(summary)-over-len(cut))) + cut
		if over = len(line(summary, files)) - maxLine; over > 0 {
			// A cut list ends in ... so that it does not read as the whole list.
			const more = ",..."
			files = short(files, max(0, len(files)-over-len(more)))
			if i := strings.LastIndex(files, ","); i >= 0 {
				files = files[:i] + more
			} else {
				files = more[1:]
			}
		}
	}
	return line(summary, files)
}

// answerPointer names the answer as a path from the current folder, which differs
// from the state folder in a linked worktree. A worker that failed wrote none.
func answerPointer(dir string) string {
	path := filepath.Join(dir, "answer.json")
	label := "answer="
	if _, err := os.Stat(path); err != nil {
		path, label = dir, "task="
	}
	return label + relativeTask(path)
}
func short(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
