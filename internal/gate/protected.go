package gate

import (
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"unicode"

	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/pathutil"
)

// RuleProtected keeps workers out of the private usage stores: the telemetry
// root and balance's state.
const RuleProtected = "private usage store"

const protectedReason = "duolog telemetry and balance stores are closed to workers"

// protectedDecision is the deny beside the spawn deny. It comes before policy,
// modes and asks, so no rule or mode can open the stores to a worker. Paths are
// compared by file identity, which covers symlinks, junctions, 8.3 names,
// /var against /private/var and case on a file system that ignores it. A path
// that does not exist yet is placed by its longest existing ancestor.
func protectedDecision(a Action, m mode.Mode) (Decision, bool) {
	deny := func(reason string) (Decision, bool) { return Decision{Deny, reason, RuleProtected, m}, true }
	if a.Kind == "exec" && protectedCommand(a.Target, a.Shell) {
		return deny("workers cannot run duolog telemetry or duolog balance; " + protectedReason)
	}
	if len(a.ProtectedRoots) == 0 || a.Target == "" || a.Kind != "read" && a.Kind != "write" && a.Kind != "exec" {
		return Decision{}, false
	}
	// Read and write targets are relative to the work root; command words to
	// the hook's working directory.
	base := a.Work
	if a.Kind == "exec" && a.Cwd != "" {
		base = a.Cwd
	}
	if base == "" {
		return deny("the gate cannot place paths without a work root; " + protectedReason)
	}
	roots := make([]protectedRoot, len(a.ProtectedRoots))
	for i, root := range a.ProtectedRoots {
		roots[i] = placeRoot(root)
	}
	reaches := func(target string, search bool) bool {
		placed := placeTarget(base, target, a.Shell)
		chain := ancestry(placed)
		for _, root := range roots {
			if root.reaches(placed, chain, search) {
				return true
			}
		}
		return false
	}
	if a.Kind != "exec" {
		if reaches(a.Target, a.Search) {
			return deny(protectedReason)
		}
		return Decision{}, false
	}
	for _, words := range protectedWords(a.Target, a.Shell) {
		for _, word := range words {
			if target := protectedWord(word); target != "" && reaches(target, false) {
				return deny(protectedReason)
			}
		}
		for _, target := range searchRoots(words) {
			if reaches(target, true) {
				return deny("a recursive search reaches " + protectedReason)
			}
		}
	}
	return Decision{}, false
}

// protectedCommand reports whether a command line runs duolog telemetry or
// duolog balance anywhere in it: by any program path, quoted, behind a
// wrapper, in a pipeline or after the PowerShell call operator, and behind
// duolog's global flags, such as --lead L or --lead=L.
func protectedCommand(command, shell string) bool {
	for _, words := range protectedWords(command, shell) {
		for i, word := range words {
			if programName(word) == "duolog" && storeSubcommand(words[i+1:]) {
				return true
			}
		}
	}
	return false
}

// storeSubcommand reports whether duolog's arguments name the telemetry or
// balance subcommand. Before the subcommand cobra reads --name and -x as
// flags that take the next word as their value, and --name=value as one
// word, so the subcommand is the first word left. A skipped value that names
// a store subcommand counts too: the deny does not depend on which flags
// take a value. Redirects are skipped, as the shell removes them before
// duolog starts.
func storeSubcommand(args []string) bool {
	store := func(word string) bool {
		sub := strings.ToLower(strings.Trim(word, `"'`))
		return strings.HasPrefix(sub, "telemetry") || strings.HasPrefix(sub, "balance")
	}
	for j := 0; j < len(args); j++ {
		word := strings.Trim(args[j], `"'`)
		if redirect := strings.TrimLeft(word, "0123456789&"); strings.HasPrefix(redirect, "<") || strings.HasPrefix(redirect, ">") {
			// A bare operator takes the next word as its file.
			if strings.Trim(redirect, "<>&|") == "" {
				j++
			}
			continue
		}
		if !strings.HasPrefix(word, "-") {
			return store(word)
		}
		if takesValue := !strings.Contains(word, "=") && (strings.HasPrefix(word, "--") || len(word) == 2); takesValue && j+1 < len(args) {
			j++
			if store(args[j]) {
				return true
			}
		}
	}
	return false
}

// protectedWords splits a command line twice, so that a deny cannot depend on
// how a shell would read it: once by its quotes, which keeps a quoted path with
// spaces whole, and once ignoring them, which opens up a command quoted for a
// wrapper such as sh -c '...'.
func protectedWords(command, shell string) [][]string {
	var lists [][]string
	for _, part := range rawExecParts(command) {
		if shell != "powershell" {
			// A POSIX shell reads "\ " as a space inside the word.
			part = strings.ReplaceAll(part, `\ `, quotedSpace)
		}
		words := quotedWords(part)
		for i := range words {
			words[i] = strings.ReplaceAll(words[i], quotedSpace, " ")
		}
		lists = append(lists, words)
	}
	var words []string
	flush := func() {
		if len(words) > 0 {
			lists = append(lists, words)
			words = nil
		}
	}
	start := -1
	for i, r := range command + "\n" {
		separator := strings.ContainsRune(";|&(){}`\n", r)
		if unicode.IsSpace(r) || separator || strings.ContainsRune(`'"<>`, r) {
			if start >= 0 {
				words = append(words, command[start:i])
				start = -1
			}
			if separator {
				flush()
			}
		} else if start < 0 {
			start = i
		}
	}
	flush()
	return lists
}

// protectedWord returns the path a command word can name: an operand, the
// file of a redirect, or the value of --file=x, -Path:x or @x.
func protectedWord(word string) string {
	if trimmed := strings.TrimLeft(word, "0123456789"); strings.HasPrefix(trimmed, "<") || strings.HasPrefix(trimmed, ">") {
		word = strings.TrimLeft(trimmed, "<>&|")
	}
	if strings.HasPrefix(word, "-") && !strings.Contains(word, "=") {
		_, value, found := strings.Cut(word, ":")
		if !found {
			return ""
		}
		word = value
	}
	return pathWord(word)
}

func programName(word string) string {
	name := strings.ToLower(path.Base(pathutil.Slashes(strings.Trim(word, `"'&`))))
	for _, suffix := range []string{".exe", ".cmd", ".bat", ".ps1"} {
		name = strings.TrimSuffix(name, suffix)
	}
	return name
}

// searchRoots lists the folders that recursive searches in one command walk:
// their operands, or the working directory when they name none.
func searchRoots(words []string) []string {
	var roots []string
	for i, word := range words {
		name, args := programName(word), words[i+1:]
		var recursive bool
		switch name {
		case "find", "fd", "fdfind", "rg", "ag", "ack", "tree", "du":
			recursive = true
		case "grep", "egrep", "fgrep":
			recursive = recursiveSearch("grep", args)
		case "ls", "dir":
			recursive = slices.ContainsFunc(args, func(w string) bool {
				return w == "--recursive" || strings.EqualFold(w, "/s") || !strings.HasPrefix(w, "--") && strings.HasPrefix(w, "-") && strings.Contains(w, "R")
			})
		case "get-childitem", "gci":
			recursive = slices.ContainsFunc(args, func(w string) bool {
				w = strings.ToLower(w)
				return strings.HasPrefix(w, "-r") || strings.HasPrefix(w, "-depth")
			})
		}
		if !recursive {
			continue
		}
		// The first operand of a search is its pattern unless an option gives one.
		pattern := slices.Contains([]string{"grep", "egrep", "fgrep", "rg", "ag", "ack"}, name) && !explicitPattern(args) && !slices.Contains(args, "--files")
		found := false
		for j := 0; j < len(args); j++ {
			if strings.HasPrefix(args[j], "-") {
				if separateValue(name, args[j]) {
					j++
				}
				continue
			}
			if pattern {
				pattern = false
				continue
			}
			roots = append(roots, args[j])
			found = true
		}
		if !found {
			roots = append(roots, ".")
		}
	}
	return roots
}

// homeVariables are the spellings of the folders a store lives under that a
// shell expands at the start of a path.
var homeVariables = []struct{ prefix, env string }{
	{"${HOME}", ""}, {"$HOME", ""}, {"$env:HOME", ""}, {"$env:USERPROFILE", "USERPROFILE"}, {"%USERPROFILE%", "USERPROFILE"},
	{"$env:LOCALAPPDATA", "LOCALAPPDATA"}, {"%LOCALAPPDATA%", "LOCALAPPDATA"}, {"$LOCALAPPDATA", "LOCALAPPDATA"},
	{"${XDG_STATE_HOME}", "XDG_STATE_HOME"}, {"$XDG_STATE_HOME", "XDG_STATE_HOME"},
}

// expandProtected expands a leading ~ or home variable. Other variables stay,
// and the rest of the gate asks about them.
func expandProtected(target string) string {
	if target == "~" || strings.HasPrefix(target, `~\`) {
		target = "~/" + strings.TrimPrefix(target[1:], `\`)
	}
	target = expandHome(target)
	for _, v := range homeVariables {
		if len(target) < len(v.prefix) || !strings.EqualFold(target[:len(v.prefix)], v.prefix) {
			continue
		}
		if rest := target[len(v.prefix):]; rest != "" && rest[0] != '/' && rest[0] != '\\' {
			continue
		}
		value := os.Getenv(v.env)
		if v.env == "" {
			value, _ = os.UserHomeDir()
		}
		if value != "" {
			return value + target[len(v.prefix):]
		}
	}
	return target
}

// localPath rewrites the Windows spellings of a local path as the drive path:
// \\?\C:\x, \\.\C:\x, \\?\UNC\localhost\C$\x and \\127.0.0.1\C$\x.
func localPath(target string) string {
	s := pathutil.Slashes(target)
	if lower := strings.ToLower(s); strings.HasPrefix(lower, "//?/unc/") {
		s = "//" + s[len("//?/unc/"):]
	} else if strings.HasPrefix(lower, "//?/") || strings.HasPrefix(lower, "//./") {
		s = s[len("//?/"):]
	}
	for _, host := range []string{"//localhost/", "//127.0.0.1/", "//[::1]/"} {
		rest := s[min(len(host), len(s)):]
		if strings.EqualFold(s[:min(len(host), len(s))], host) && len(rest) >= 2 && rest[1] == '$' && unicode.IsLetter(rune(rest[0])) && (len(rest) == 2 || rest[2] == '/') {
			s = rest[:1] + ":" + rest[2:]
		}
	}
	if s == pathutil.Slashes(target) {
		return target
	}
	return s
}

// placeTarget makes a target absolute against base. A Windows path loses its
// stream name and the trailing dots and spaces Windows drops; a Windows path
// on another host stays lexical.
func placeTarget(base, target, shell string) string {
	target = localPath(expandProtected(target))
	if runtime.GOOS == "windows" || shell == "windows" || shell == "powershell" || pathutil.Windows(target) {
		drive := windowsDrive.FindString(target)
		rest := target[len(drive):]
		if i := strings.IndexByte(rest, ':'); i >= 0 {
			rest = rest[:i]
		}
		target, _ = windowsTarget(drive + rest)
	}
	if runtime.GOOS != "windows" && (pathutil.Windows(target) || pathutil.Windows(base)) {
		if !pathutil.IsAbs(target) {
			target = pathutil.Slashes(base) + "/" + target
		}
		return pathutil.Clean(target)
	}
	native := pathutil.Native(target)
	if !filepath.IsAbs(native) {
		if strings.HasPrefix(native, string(filepath.Separator)) {
			// \x on Windows is on the working directory's drive.
			native = filepath.VolumeName(base) + native
		} else {
			// A shell resolves .. physically, so the base is resolved first.
			native = filepath.Join(pathutil.Resolved(pathutil.Native(base)), native)
		}
	}
	return filepath.Clean(native)
}

// link is an existing folder on a path, with the rest of the path below it.
type link struct {
	info os.FileInfo
	rest []string
}

// ancestry lists the existing ancestors of a path, itself included, from the
// deepest up. A Windows path on another host has none.
func ancestry(name string) []link {
	if runtime.GOOS != "windows" && pathutil.Windows(name) {
		return nil
	}
	var links []link
	var rest []string
	for dir := name; ; {
		if info, err := os.Stat(dir); err == nil {
			links = append(links, link{info, slices.Clone(rest)})
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return links
		}
		rest = append([]string{filepath.Base(dir)}, rest...)
		dir = parent
	}
}

// protectedRoot is a store root placed on the file system, whether or not it
// exists yet.
type protectedRoot struct {
	lexical []string
	links   []link
	fold    bool
}

func placeRoot(root string) protectedRoot {
	r := protectedRoot{lexical: strings.Split(pathutil.Clean(root), "/"), links: ancestry(filepath.Clean(pathutil.Native(root)))}
	r.fold = runtime.GOOS == "darwin" || runtime.GOOS == "windows" || pathutil.Windows(root)
	if len(r.links) > 0 {
		r.fold = foldsCase(root, r.links[0])
	}
	return r
}

// foldsCase asks the file system whether it ignores case: the deepest existing
// folder of the root, spelled with its case swapped, is the same folder.
func foldsCase(root string, l link) bool {
	dir := filepath.Clean(pathutil.Native(root))
	for range l.rest {
		dir = filepath.Dir(dir)
	}
	name := filepath.Base(dir)
	swapped := strings.Map(func(r rune) rune {
		if unicode.IsUpper(r) {
			return unicode.ToLower(r)
		}
		return unicode.ToUpper(r)
	}, name)
	if swapped == name {
		return runtime.GOOS == "darwin" || runtime.GOOS == "windows"
	}
	info, err := os.Stat(filepath.Join(filepath.Dir(dir), swapped))
	return err == nil && os.SameFile(info, l.info)
}

// reaches reports whether a placed target is in the root, or, for a search, at
// or above it. chain is the target's ancestry.
func (r protectedRoot) reaches(target string, chain []link, search bool) bool {
	match := func(target, root []string) bool {
		inside, above := relation(target, root, r.fold)
		return inside || search && above
	}
	if match(strings.Split(pathutil.Clean(target), "/"), r.lexical) {
		return true
	}
	for _, t := range chain {
		for _, l := range r.links {
			if os.SameFile(t.info, l.info) && match(t.rest, l.rest) {
				return true
			}
		}
	}
	return false
}

// relation compares two paths by segment from a shared folder. The target's
// segments may be globs, and ** reaches anything below.
func relation(target, root []string, fold bool) (inside, above bool) {
	for i, segment := range target {
		if strings.Contains(segment, "**") {
			return true, true
		}
		if i >= len(root) {
			return true, false
		}
		if !segmentMatch(segment, root[i], fold) {
			return false, false
		}
	}
	return len(target) == len(root), len(target) < len(root)
}

func segmentMatch(pattern, name string, fold bool) bool {
	if fold {
		pattern, name = strings.ToLower(pattern), strings.ToLower(name)
	}
	if !strings.ContainsAny(pattern, "*?[{") {
		return pattern == name
	}
	for _, p := range expandBraces(pattern) {
		// A malformed pattern fails closed.
		if ok, err := path.Match(p, name); ok || err != nil {
			return true
		}
	}
	return false
}
