package policy

import (
	"path/filepath"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
)

// leadMode is m lowered to the lead's mode, when the lead's rules name one.
func leadMode(m mode.Mode, lead LeadRules) mode.Mode {
	if lead.Mode != "" {
		return mode.Lower(m, lead.Mode)
	}
	return m
}

// withoutCdIntoCwd is an exec action with DropCdIntoCwd applied to its line,
// from its folder, or the work root when it names none.
func withoutCdIntoCwd(a Action) Action {
	if a.Kind != "exec" {
		return a
	}
	cwd := a.Cwd
	if cwd == "" {
		cwd = a.Work
	}
	a.Target = DropCdIntoCwd(a.Target, cwd, a.Shell)
	return a
}

// DropCdIntoCwd returns command without a leading cd DIR && when DIR is the
// folder cwd that the command already runs in, and the command as it is
// otherwise. Such a cd changes nothing, as a task's Check that names its own
// worktree does, but any cd in a line makes the line uncertain. DIR is one
// plain word, quoted or not, with no expansion, glob or escape; a relative
// DIR is placed from cwd. Only a POSIX line is read.
func DropCdIntoCwd(command, cwd, shell string) string {
	if cwd == "" || shell == "powershell" || shell == "windows" {
		return command
	}
	dir, tail, ok := cdPrefix(command)
	if !ok || !sameFolder(dir, cwd) {
		return command
	}
	return tail
}

// cdPrefix splits a line that starts with cd DIR && into DIR, without the
// quotes of a word quoted as a whole, and the rest of the line. ok is false
// for any other line, a DIR that is no plain word, or nothing after &&.
func cdPrefix(command string) (dir, tail string, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimLeft(command, " \t"), "cd")
	if !found || !strings.HasPrefix(rest, " ") && !strings.HasPrefix(rest, "\t") {
		return "", "", false
	}
	dir, after, found := strings.Cut(strings.TrimLeft(rest, " \t"), " ")
	tail, found2 := strings.CutPrefix(strings.TrimLeft(after, " \t"), "&&")
	tail = strings.TrimLeft(tail, " \t")
	if !found || !found2 || tail == "" {
		return "", "", false
	}
	if len(dir) >= 2 && (dir[0] == '\'' || dir[0] == '"') && dir[len(dir)-1] == dir[0] {
		dir = dir[1 : len(dir)-1]
	}
	plain := dir != "" && !strings.HasPrefix(dir, "-") && !strings.ContainsAny(dir, "'\"\\$`*?[]{}~!#&|;<>()=%\t")
	return dir, tail, plain
}

// sameFolder reports whether dir, placed from cwd when it is relative, is the
// folder cwd once both are cleaned and their symlinks resolved.
func sameFolder(dir, cwd string) bool {
	native := pathutil.Native(dir)
	if !filepath.IsAbs(native) {
		native = filepath.Join(pathutil.Native(cwd), native)
	}
	return pathutil.Resolved(filepath.Clean(native)) == pathutil.Resolved(filepath.Clean(pathutil.Native(cwd)))
}
