package policy

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
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
		relative, err := filepath.Rel(pathutil.Resolved(a.Work), literalResolved(physicalJoin(cwd, target)))
		if err != nil {
			return true
		}
		target = relative
	}
	target = path.Clean(target)
	return strings.HasPrefix(target, "/") || len(target) >= 2 && target[1] == ':' || target == ".." || strings.HasPrefix(target, "../") || strings.HasPrefix(target, "~")
}

// literalResolved is resolvedTarget for a POSIX name, whose backslashes are
// characters of a name rather than the mark of a foreign Windows path.
func literalResolved(name string) string {
	return followLinks(name, maxLinkHops)
}

// maxLinkHops bounds how many dangling symlinks followLinks follows, as the
// kernel bounds a chain of symlinks; a longer chain stays where it stands.
const maxLinkHops = 40

// resolvedTarget is pathutil.Resolved, except that a symlink whose target does
// not exist yet leads to that target: a write through it, as echo x > link
// does, creates the file there. Foreign Windows paths stay lexical on a POSIX
// host.
func resolvedTarget(name string) string {
	if runtime.GOOS != "windows" && pathutil.Windows(name) {
		return name
	}
	return followLinks(name, maxLinkHops)
}

// followLinks resolves the existing prefix of name, and follows a dangling
// symlink in it to its target, at most hops times.
func followLinks(name string, hops int) string {
	if resolved, err := filepath.EvalSymlinks(name); err == nil {
		return resolved
	}
	parent := filepath.Dir(name)
	if parent == name {
		return name
	}
	parent = followLinks(parent, hops)
	if link, ok := danglingLink(name, parent); ok && hops > 0 {
		return followLinks(link, hops-1)
	}
	return filepath.Join(parent, filepath.Base(name))
}

// danglingLink returns where name points when it is a symlink, with a
// relative target placed in parent, the resolved folder of name.
func danglingLink(name, parent string) (string, bool) {
	info, err := os.Lstat(name)
	if err != nil || info.Mode()&fs.ModeSymlink == 0 {
		return "", false
	}
	link, err := os.Readlink(name)
	if err != nil {
		return "", false
	}
	return physicalJoin(parent, link), true
}

// physicalJoin places name, absolute or relative to dir, and returns it
// without . or ..: a .. right after a component of name that is a symlink
// goes up from the symlink's target, as the kernel goes, where filepath.Join
// would drop the symlink as text. Every other .. drops the component before
// it, as Join does: callers spell a relative path from dir as written, so a
// .. that leaves dir itself stays as text.
func physicalJoin(dir, name string) string {
	native := filepath.FromSlash(name)
	if !slices.Contains(strings.Split(filepath.ToSlash(native), "/"), "..") {
		if filepath.IsAbs(native) {
			return filepath.Clean(native)
		}
		return filepath.Join(dir, native)
	}
	current := dir
	if filepath.IsAbs(native) {
		volume := filepath.VolumeName(native)
		current, native = volume+string(filepath.Separator), native[len(volume):]
	}
	if !filepath.IsAbs(current) {
		// A relative folder has no place to walk from; .. stays as written.
		return filepath.Join(dir, native)
	}
	// added counts the components of name that current ends in.
	added := 0
	for _, part := range strings.Split(filepath.ToSlash(native), "/") {
		switch {
		case part == "" || part == ".":
		case part != "..":
			current, added = filepath.Join(current, part), added+1
		case added > 0 && isLink(current):
			// From here on current is resolved, so text is where the kernel goes.
			current, added = filepath.Dir(followLinks(current, maxLinkHops)), 0
		default:
			current, added = filepath.Dir(current), max(added-1, 0)
		}
	}
	return current
}

// isLink reports whether name is a symlink, or on Windows a junction or other
// reparse point, which Go reports as irregular.
func isLink(name string) bool {
	info, err := os.Lstat(name)
	return err == nil && (info.Mode()&fs.ModeSymlink != 0 || runtime.GOOS == "windows" && info.Mode()&fs.ModeIrregular != 0)
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
	path := physicalJoin(cwd, pathutil.Native(target))
	root, path = pathutil.Resolved(root), resolvedTarget(path)
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
