package gate

import (
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/pathutil"
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
	rgValueShorts   = "ABCEMTdefgjmrt"
	rgPlainShorts   = ".0FHILNPSUVabchilnopqsuvwx"
	grepValueShorts = "ABCDXdefm"
	// jq options that only change the output. -f, -L, --rawfile and --slurpfile
	// read other files, and an error message prints the program it read.
	jqPlainLong = []string{"--null-input", "--raw-input", "--slurp", "--compact-output", "--raw-output", "--raw-output0", "--join-output", "--ascii-output",
		"--sort-keys", "--color-output", "--monochrome-output", "--exit-status", "--tab", "--seq", "--stream", "--stream-errors"}
	jqPlainShorts = "nrjascCMSeR"
	// The number of words each jq option with values takes.
	jqValueLong = map[string]int{"--indent": 1, "--arg": 2, "--argjson": 2}
	// sort options that only change the order. -o writes, --compress-program
	// runs a program, and -T writes elsewhere; GNU also accepts any unique
	// prefix of a long option, so only these exact names are known.
	sortPlainLong = []string{"--ignore-leading-blanks", "--dictionary-order", "--ignore-case", "--general-numeric-sort", "--human-numeric-sort", "--ignore-nonprinting",
		"--month-sort", "--numeric-sort", "--random-sort", "--reverse", "--version-sort", "--stable", "--unique", "--zero-terminated", "--merge", "--debug"}
	sortValueLong   = []string{"--sort", "--key", "--field-separator", "--check", "--buffer-size", "--parallel"}
	sortPlainShorts = "bdfghiMnRrVsuzcCm"
	sortValueShorts = "ktS"
)

// jqFiles returns the files a jq command reads, after its filter, and false
// when an option is not known to be a plain read.
func jqFiles(args []string) ([]string, bool) {
	var files []string
	filter, literal := false, false
	for i := 0; i < len(args); i++ {
		word := args[i]
		switch {
		case literal || word == "-" || !strings.HasPrefix(word, "-"):
			if filter {
				files = append(files, word)
			}
			filter = true
		case word == "--":
			literal = true
		case jqValueLong[word] > 0:
			i += jqValueLong[word]
		case strings.HasPrefix(word, "--"):
			if !slices.Contains(jqPlainLong, word) {
				return nil, false
			}
		default:
			if strings.Trim(word[1:], jqPlainShorts) != "" {
				return nil, false
			}
		}
	}
	return files, true
}

func sortOptionsKnown(words []string) bool {
	for i := 0; i < len(words); i++ {
		word := words[i]
		switch {
		case word == "--":
			return true
		case !strings.HasPrefix(word, "-") || word == "-":
		case strings.HasPrefix(word, "--"):
			name, _, attached := strings.Cut(word, "=")
			if slices.Contains(sortValueLong, name) {
				if !attached && name != "--check" {
					i++
				}
			} else if attached || !slices.Contains(sortPlainLong, name) {
				return false
			}
		default:
			for j, letter := range word[1:] {
				if strings.ContainsRune(sortValueShorts, letter) {
					// The value is the rest of the word, or the next word.
					if j == len(word)-2 {
						i++
					}
					break
				}
				if !strings.ContainsRune(sortPlainShorts, letter) {
					return false
				}
			}
		}
	}
	return true
}

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
	for i := 0; i < len(words); i++ {
		word := words[i]
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
		// -e -- or --regexp -- : the next word is this option's value, not an option or the end of options.
		if separateValue("rg", word) {
			i++
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
	case "pwd", "get-location":
		return nil, ""
	case "git":
		return gitTargets(args)
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
	case "jq":
		files, _ := jqFiles(args)
		return checkTargets(files)
	}
	if recursiveSearch(name, args) {
		return nil, "a recursive search can read files that policy denies; name the files or use rg without --hidden, -u or --no-ignore"
	}
	// The first word is a search pattern, not a path.
	skipPattern := name == "grep" || name == "rg" || name == "select-string"
	if skipPattern && (explicitPattern(args) || name == "rg" && slices.Contains(args, "--files")) {
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
			// -g X and --glob X: the next word is the option's value, not a path.
			if separateValue(name, word) && i+1 < len(args) {
				i++
				if strings.ContainsAny(args[i], "*?[{") || strings.HasPrefix(args[i], "~") {
					return nil, "a shell glob or brace in an option value can expand onto files that policy denies; name the files"
				}
				// Only -f/--file names a file; -e, -g, -A and the like take a pattern or a number.
				if fileValue(name, word) {
					values = append(values, args[i])
				}
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

// gitTargets lists what a Git read names. A word may be a revision instead of a
// path, which read rules allow, and rev:path reads that path from the history.
// git diff and git log with no path still show tracked denied files: like plain
// rg, that is a search the gate cannot see into.
func gitTargets(args []string) ([]string, string) {
	if len(args) == 0 {
		return nil, ""
	}
	var targets []string
	literal := false
	for _, word := range args[1:] {
		switch {
		case literal:
			targets = append(targets, word)
		case word == "--":
			literal = true
		case strings.HasPrefix(word, "-"):
		default:
			if _, path, found := strings.Cut(word, ":"); found && !pathutil.IsAbs(word) {
				word = path
			}
			targets = append(targets, word)
		}
	}
	return checkTargets(targets)
}

func mergeTargets(targets, values []string) ([]string, string) {
	targets, reason := checkTargets(targets)
	if reason != "" {
		return nil, reason
	}
	// An option value such as --include=*.go is a filter, not a shell glob, and
	// only a value that names a plain path can be judged.
	for _, value := range values {
		// Braces expand even inside an option word: -f{x,.env} reads .env.
		if strings.Contains(value, "{") {
			return nil, "a brace in an option value can expand onto files that policy denies; name the files"
		}
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
		letter, value := valueShort(command, word)
		if letter == 'f' {
			return value
		}
		return ""
	case "get-content", "get-childitem", "get-item", "test-path", "select-string":
		_, value, _ := strings.Cut(word, ":")
		return value
	}
	_, value, _ := strings.Cut(word, "=")
	return value
}

// valueShort finds the option in a short cluster that takes a value, and the
// value written in the same word: -nfx is -n with -f x. Validation and path
// reading both use it, so that a cluster cannot hide a file from one of them.
func valueShort(command, word string) (rune, string) {
	if !strings.HasPrefix(word, "-") || strings.HasPrefix(word, "--") {
		return 0, ""
	}
	letters := rgValueShorts
	if command == "grep" {
		letters = grepValueShorts
	}
	cluster := word[1:]
	for i, letter := range cluster {
		if strings.ContainsRune(letters, letter) {
			return letter, cluster[i+1:]
		}
	}
	return 0, ""
}

var (
	rgSeparateLong = []string{"--after-context", "--before-context", "--context", "--glob", "--iglob", "--type", "--type-not", "--max-count", "--max-depth", "--max-columns",
		"--regexp", "--file", "--encoding", "--sort", "--sortr", "--threads", "--replace", "--engine", "--color", "--colors", "--context-separator", "--dfa-size-limit",
		"--field-context-separator", "--field-match-separator", "--max-filesize", "--path-separator", "--regex-size-limit"}
	// grep's --context and -C take an optional value that must be attached (--context=3, -C3), so they never consume the next word.
	grepSeparateLong = []string{"--after-context", "--before-context", "--regexp", "--file", "--max-count", "--include", "--exclude", "--exclude-dir", "--label"}
)

// separateValue reports whether word is an option whose value is the next
// word: a short cluster ending in a value letter, or a long option without =.
func separateValue(command, word string) bool {
	if command != "rg" && command != "grep" {
		return false
	}
	if strings.HasPrefix(word, "--") {
		if strings.Contains(word, "=") {
			return false
		}
		if command == "rg" {
			return slices.Contains(rgSeparateLong, word)
		}
		return slices.Contains(grepSeparateLong, word)
	}
	letter, value := valueShort(command, word)
	if command == "grep" && letter == 'C' {
		return false
	}
	return letter != 0 && value == ""
}

// fileValue reports whether an option's separate value is a file to read.
func fileValue(command, word string) bool {
	if strings.HasPrefix(word, "--") {
		return word == "--file"
	}
	letter, _ := valueShort(command, word)
	return letter == 'f'
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
		if letter, _ := valueShort("grep", word); strings.HasPrefix(word, "-") && (letter == 'e' || letter == 'f') {
			return true
		}
		if letter, _ := valueShort("rg", word); strings.HasPrefix(word, "-") && (letter == 'e' || letter == 'f') {
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
			if name == "--directories" || !long && strings.Contains(word, "d") {
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
