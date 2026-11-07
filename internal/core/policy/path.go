package policy

import (
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
)

// outside reports whether a path leaves the work root; see pathutil.Outside.
func outside(path string) bool {
	return pathutil.Outside(path)
}

// workTarget places a path that a command in a names, as written, from a's
// work root, as RelativeTarget does.
func workTarget(a Action, target string) string {
	if a.Work == "" {
		return target
	}
	return RelativeTarget(a.Work, a.Cwd, expandHome(target))
}

// posixShell reports whether a's command runs in a POSIX shell on a POSIX
// host, where a backslash is never a path separator. PowerShell, cmd and any
// shell on Windows read it as one.
func posixShell(a Action) bool {
	return runtime.GOOS != "windows" && a.Shell != "powershell" && a.Shell != "windows"
}

// escaped lists the characters a POSIX shell gives a meaning of their own, so
// a backslash in front of one is an escape: \{ is {, and \; is ;. A backslash
// in front of any other character, a backslash among them, may separate the
// folders of a Windows path, as in \x or C:\x.
const escaped = "{}[]()<>;&|*?$`'\"!#~"

// withoutEscapes removes the backslash of each escape in word, outside single
// quotes, as a POSIX shell does, Git Bash on Windows too, before the word
// is judged as a path: grep \{a,b} searches for {a,b}, not for \{a,b} at the
// root of the drive. PowerShell and cmd have no such escapes. Inside double
// quotes only \$, \` and \" are escapes.
func withoutEscapes(a Action, word string) string {
	if a.Shell == "powershell" || a.Shell == "windows" || !strings.Contains(word, `\`) {
		return word
	}
	var b strings.Builder
	var quote byte
	for i := 0; i < len(word); i++ {
		c := word[i]
		switch {
		case quote == '\'':
			if c == quote {
				quote = 0
			}
		case c == '\\' && escapes(word[i+1:], quote):
			i++
			c = word[i]
		case c == quote:
			quote = 0
		case quote == 0 && (c == '\'' || c == '"'):
			quote = c
		}
		b.WriteByte(c)
	}
	return b.String()
}

// escapes reports whether a backslash in front of rest, outside quotes or
// inside the quote given, is an escape.
func escapes(rest string, quote byte) bool {
	if rest == "" || strings.IndexByte(escaped, rest[0]) < 0 {
		return false
	}
	return quote == 0 || strings.IndexByte("$`\"", rest[0]) >= 0
}

// literalOutside reports whether target, a command word, names a path outside
// a's work root when its backslashes are characters of a file name. A work
// root or folder written as a Windows path keeps RelativeTarget's reading. A
// drive letter still names a Windows root.
func literalOutside(a Action, target string) bool {
	if a.Work != "" {
		cwd := a.Cwd
		if cwd == "" {
			cwd = a.Work
		}
		if pathutil.Windows(a.Work) || pathutil.Windows(cwd) {
			return outside(RelativeTarget(a.Work, a.Cwd, target))
		}
		name := target
		if !filepath.IsAbs(name) {
			name = filepath.Join(cwd, name)
		}
		relative, err := filepath.Rel(pathutil.Resolved(a.Work), literalResolved(name))
		if err != nil {
			return true
		}
		target = relative
	}
	target = path.Clean(target)
	return strings.HasPrefix(target, "/") || len(target) >= 2 && target[1] == ':' || target == ".." || strings.HasPrefix(target, "../") || strings.HasPrefix(target, "~")
}

// literalResolved is pathutil.Resolved for a POSIX name, whose backslashes
// are characters of a name rather than the mark of a foreign Windows path.
func literalResolved(name string) string {
	if resolved, err := filepath.EvalSymlinks(name); err == nil {
		return resolved
	}
	parent := filepath.Dir(name)
	if parent == name {
		return name
	}
	return filepath.Join(literalResolved(parent), filepath.Base(name))
}

// onCurrentDrive spells a rooted path without a drive, such as /etc/passwd or
// \x, on the drive or share of cwd: Windows reads it from the root of the
// current drive, not from the current folder. A path with a drive or a share,
// a relative path, and any path under a cwd without a drive, as on a POSIX
// host, stay as written.
func onCurrentDrive(cwd, target string) string {
	rooted := pathutil.Slashes(target)
	if !strings.HasPrefix(rooted, "/") || strings.HasPrefix(rooted, "//") {
		return target
	}
	folder := pathutil.Slashes(cwd)
	if len(folder) >= 2 && folder[1] == ':' {
		return folder[:2] + rooted
	}
	if share := strings.SplitN(strings.TrimPrefix(folder, "//"), "/", 3); strings.HasPrefix(folder, "//") && len(share) >= 2 {
		return "//" + share[0] + "/" + share[1] + rooted
	}
	return target
}

// RelativeTarget compares real filesystem roots, including OS aliases such as
// /var and /private/var, and parents of files that have not been created yet.
// A rooted path without a drive is on the drive of cwd, as onCurrentDrive
// spells it.
func RelativeTarget(root, cwd, target string) string {
	if cwd == "" {
		cwd = root
	}
	target = onCurrentDrive(cwd, target)
	// Foreign Windows paths cannot be resolved by the host filesystem.
	if runtime.GOOS != "windows" && (pathutil.Windows(root) || pathutil.Windows(cwd) || pathutil.IsAbs(target) && pathutil.Windows(target)) {
		target = pathutil.Clean(target)
		if !pathutil.IsAbs(target) {
			target = pathutil.Clean(pathutil.Slashes(cwd) + "/" + target)
		}
		rel, err := pathutil.Relative(root, target)
		if err != nil {
			return target
		}
		return rel
	}
	path := pathutil.Native(target)
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	root, path = pathutil.Resolved(root), pathutil.Resolved(path)
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return pathutil.Clean(path)
	}
	return pathutil.Clean(relative)
}

// toolPath returns a read or write target as the rules compare it, or the
// reason to deny a spelling that no rule can judge.
func toolPath(a Action) (Action, string) {
	if pathutil.Windows(a.Target) {
		a.Shell = "windows"
	}
	if usesWindowsNames(a) {
		target, ok := windowsTarget(a.Target)
		if !ok {
			return a, "a Windows stream name can reach a denied file"
		}
		a.Target = target
	}
	if !a.revision && shortNamePath(a.Target) {
		return a, "a Windows 8.3 name, such as DUOLOG~1, can reach a denied file; use the long name"
	}
	a.Target = cleanPath(a.Target)
	return a, ""
}

// foldCase reports whether rule r compares paths without case. A deny does on
// every host: a Linux host can work on an NTFS or FAT folder, where .DUOLOG is
// .duolog. An allow does only where names ignore case.
func foldCase(r config.Rule, a Action) bool {
	if r.Decision != Allow && r.Decision != Ask {
		return true
	}
	return runtime.GOOS == "darwin" || runtime.GOOS == "windows" || a.Shell == "windows" || pathutil.Windows(r.Match) || pathutil.Windows(a.Target)
}
