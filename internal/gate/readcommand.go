package gate

import (
	"slices"
	"strings"
)

// The options listed here only change what is printed. Anything else, in a
// command named as read-only, is not a read: find and rg have options that
// write files or run programs, and new ones can appear in any release.
var (
	findOptions = []string{
		"-h", "-l", "-p", "-a", "-and", "-o", "-or", "-not", "-print", "-print0", "-printf", "-ls", "-prune", "-quit",
		"-name", "-iname", "-path", "-ipath", "-wholename", "-iwholename", "-lname", "-ilname", "-regex", "-iregex", "-regextype",
		"-type", "-xtype", "-size", "-empty", "-perm", "-user", "-group", "-uid", "-gid", "-nouser", "-nogroup", "-links", "-inum", "-samefile",
		"-mtime", "-mmin", "-atime", "-amin", "-ctime", "-cmin", "-daystart", "-newer", "-anewer", "-cnewer",
		"-maxdepth", "-mindepth", "-depth", "-d", "-follow", "-xdev", "-mount", "-readable", "-writable", "-executable", "-true", "-false",
	}
	rgLongOptions = []string{
		"--after-context", "--before-context", "--binary", "--block-buffered", "--byte-offset", "--case-sensitive", "--color", "--colors", "--column", "--context", "--context-separator",
		"--count", "--count-matches", "--crlf", "--debug", "--dfa-size-limit", "--encoding", "--engine", "--field-context-separator", "--field-match-separator", "--files", "--files-with-matches",
		"--files-without-match", "--fixed-strings", "--follow", "--glob", "--glob-case-insensitive", "--heading", "--help", "--hidden", "--iglob", "--ignore-case", "--ignore-file-case-insensitive",
		"--include-zero", "--invert-match", "--json", "--line-buffered", "--line-number", "--line-regexp", "--max-columns", "--max-columns-preview", "--max-count", "--max-depth", "--max-filesize",
		"--mmap", "--multiline", "--multiline-dotall", "--no-context-separator", "--no-filename", "--no-heading", "--no-ignore", "--no-ignore-dot", "--no-ignore-exclude", "--no-ignore-files",
		"--no-ignore-global", "--no-ignore-parent", "--no-ignore-vcs", "--no-line-number", "--no-messages", "--no-mmap", "--no-unicode", "--null", "--null-data", "--one-file-system",
		"--only-matching", "--passthru", "--path-separator", "--pretty", "--quiet", "--regex-size-limit", "--regexp", "--file", "--replace", "--smart-case", "--sort", "--sortr", "--stats",
		"--text", "--threads", "--trim", "--type", "--type-list", "--type-not", "--unrestricted", "--vimgrep", "--with-filename", "--word-regexp", "--version",
	}
	rgValueShorts = "ABCEMTdefgjmrt"
	rgPlainShorts = ".0FHILNPSUVabchilnopqsuvwx"
)

func findOptionsKnown(words []string) bool {
	for _, word := range words {
		// A value such as the -1 of -mtime -1 is not an option.
		if !strings.HasPrefix(word, "-") || strings.Trim(word[1:], "0123456789") == "" {
			continue
		}
		// -newerXY names its two time kinds inside the option.
		if !slices.Contains(findOptions, word) && !(strings.HasPrefix(word, "-newer") && len(word) == len("-newerXY")) {
			return false
		}
	}
	return true
}

func rgOptionsKnown(words []string) bool {
	for _, word := range words {
		switch {
		case word == "--":
			return true
		case !strings.HasPrefix(word, "-") || word == "-":
		case strings.HasPrefix(word, "--"):
			name, _, _ := strings.Cut(word, "=")
			if !slices.Contains(rgLongOptions, name) {
				return false
			}
		default:
			if !rgShortsKnown(word[1:]) {
				return false
			}
		}
	}
	return true
}

// rg option letters depend on case, so this reads the command as written. A
// letter that takes a value ends the cluster: the rest of the word is the value.
func rgShortsKnown(cluster string) bool {
	for _, letter := range cluster {
		switch {
		case strings.ContainsRune(rgValueShorts, letter):
			return true
		case !strings.ContainsRune(rgPlainShorts, letter):
			return false
		}
	}
	return true
}

// readCommandTargets lists the paths a read-only command reads. A non-empty
// reason means that no list of paths can be trusted, so a person has to decide.
func readCommandTargets(command string) (targets []string, reason string) {
	words := strings.Fields(command)
	if len(words) == 0 {
		return nil, ""
	}
	name, args := strings.ToLower(words[0]), words[1:]
	switch name {
	case "git", "pwd", "get-location":
		return nil, ""
	case "find":
		for _, word := range args {
			if strings.HasPrefix(word, "-") && !isFindSymlinkOption(word) {
				break
			}
			if !strings.HasPrefix(word, "-") {
				targets = append(targets, word)
			}
		}
		return checkTargets(targets)
	}
	if recursiveSearch(name, args) {
		return nil, "a recursive search can read files that policy denies; name the files or use rg without --hidden, -u or --no-ignore"
	}
	skipPattern := name == "grep" || name == "rg" || name == "select-string"
	if skipPattern && explicitPattern(args) {
		skipPattern = false
	}
	var values []string
	for i := 0; i < len(args); i++ {
		word := args[i]
		switch {
		case word == "-":
		case word == "--":
			for _, rest := range args[i+1:] {
				if skipPattern {
					skipPattern = false
					continue
				}
				targets = append(targets, rest)
			}
			return mergeTargets(targets, values)
		case strings.HasPrefix(word, "-"):
			if value := optionValue(name, word); value != "" {
				values = append(values, value)
			}
			// The word after -Pattern is the pattern, not a path.
			if strings.EqualFold(word, "-pattern") {
				i++
			}
		case skipPattern:
			skipPattern = false
		default:
			targets = append(targets, word)
		}
	}
	return mergeTargets(targets, values)
}

func mergeTargets(targets, values []string) ([]string, string) {
	targets, reason := checkTargets(targets)
	if reason != "" {
		return nil, reason
	}
	// An option value such as --include=*.go is a filter, not a shell glob, and
	// only a value that names a plain path can be judged.
	for _, value := range values {
		if !strings.ContainsAny(value, "*?[{") {
			targets = append(targets, value)
		}
	}
	return targets, ""
}

func checkTargets(targets []string) ([]string, string) {
	for _, target := range targets {
		if strings.ContainsAny(target, "*?[{") {
			return nil, "a shell glob can expand onto files that policy denies; name the files"
		}
		if strings.HasPrefix(target, "~") && !strings.HasPrefix(target, "~/") {
			return nil, "a path in another home folder requires approval"
		}
	}
	return targets, ""
}

func isFindSymlinkOption(word string) bool {
	return word == "-H" || word == "-L" || word == "-P" || word == "-h" || word == "-l" || word == "-p"
}

// optionValue returns a value written inside an option word: --file=x, -f.env,
// or the PowerShell form -Path:x.
func optionValue(command, word string) string {
	if strings.HasPrefix(word, "--") {
		_, value, _ := strings.Cut(word, "=")
		return value
	}
	switch command {
	case "grep", "rg":
		if strings.HasPrefix(word, "-f") {
			return word[2:]
		}
		return ""
	case "get-content", "get-childitem", "get-item", "test-path", "select-string":
		_, value, _ := strings.Cut(word, ":")
		return value
	}
	_, value, _ := strings.Cut(word, "=")
	return value
}

func explicitPattern(args []string) bool {
	for _, word := range args {
		lower := strings.ToLower(word)
		if word == "--" {
			return false
		}
		if lower == "-pattern" || strings.HasPrefix(lower, "-pattern:") || strings.HasPrefix(lower, "--regexp") || strings.HasPrefix(lower, "--file") {
			return true
		}
		if !strings.HasPrefix(word, "--") && (strings.HasPrefix(word, "-e") || strings.HasPrefix(word, "-f")) {
			return true
		}
	}
	return false
}

func recursiveSearch(command string, args []string) bool {
	for _, word := range args {
		if word == "--" {
			return false
		}
		if !strings.HasPrefix(word, "-") {
			continue
		}
		name, _, _ := strings.Cut(word, "=")
		long := strings.HasPrefix(word, "--")
		switch command {
		case "grep":
			if name == "--recursive" || name == "--dereference-recursive" || !long && strings.ContainsAny(word, "rR") {
				return true
			}
		case "rg":
			if name == "--hidden" || name == "--unrestricted" || strings.HasPrefix(name, "--no-ignore") || !long && strings.ContainsAny(word, "u.") {
				return true
			}
		}
	}
	return false
}
