package policy

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// madeInLine reports whether another command of a's line may make target, a
// path in a temp folder that does not exist yet, or a folder above it inside
// the temp folder, a symlink or another special file before the judged
// command writes it: the gate sees the folder as it is before the line runs,
// and cp -P or ln could leave a symlink there. Only a command that names
// target or such a folder as an operand counts, and only when it is not one
// that makes no such file (linkFree). An output redirect target only ever
// makes a plain file, so it never counts.
func madeInLine(a Action, target string) bool {
	place := func(name string) string { return followLinks(filepath.Clean(name), maxLinkHops) }
	want := place(target)
	names := func(word string) bool {
		if !filepath.IsAbs(word) {
			return false
		}
		got := place(word)
		return got == want || within(got, want) && slices.ContainsFunc(a.Temp, func(temp string) bool { return temp != "" && got != temp && within(temp, got) })
	}
	return slices.ContainsFunc(rawExecParts(a.Target), func(part string) bool {
		return slices.ContainsFunc(lineOperands(part), names) && !linkFree(part)
	})
}

// linkFreeReads make no file of any kind when readOnlyCommand accepts their
// options: they read and print. rg --pre and sort --compress-program run a
// program, which readOnlyCommand refuses.
var linkFreeReads = []string{"cat", "head", "tail", "wc", "grep", "egrep", "fgrep", "rg", "sort", "uniq", "cut", "nl"}

// linkFree reports whether a command part, its program first after keywords
// and assignments, can make no symlink or other special file: mkdir and
// touch make real folders and plain files, cp copies no link
// (copiesNoLink), ls without -R, -L or a long option, diff, echo, printf and
// the programs of linkFreeReads only read or print, and sed only prints or
// substitutes. Any other program, such as ln, mv, install, tar, rsync,
// unzip, cpio, pax, git or patch, a wrapper such as xargs, and any program
// the gate does not know, may make one.
func linkFree(part string) bool {
	words := strings.Fields(commandAfterKeywords(part))
	for len(words) > 0 && execAssignment.MatchString(words[0]) {
		words = words[1:]
	}
	if len(words) == 0 {
		return true
	}
	switch program := programName(words[0]); {
	case slices.Contains([]string{"mkdir", "touch", "diff", "echo", "printf"}, program):
		return true
	case slices.Contains(linkFreeReads, program):
		return readOnlyCommand(strings.Join(words, " "))
	case program == "cp":
		return copiesNoLink(words)
	case program == "ls":
		return operandTempUse(words, len(words)-1) == tempList
	case program == "sed":
		return sedScripts(words[1:]) != nil
	}
	return false
}

// lineOperands returns the words of a command line split at blanks, with
// quotes and shell punctuation trimmed and the value of an option such as
// --x=y in place of the option, leaving out output redirects and the files
// they name.
func lineOperands(line string) []string {
	var words []string
	fields := strings.Fields(line)
	for i := 0; i < len(fields); i++ {
		if redirect := strings.TrimLeft(fields[i], "0123456789"); strings.HasPrefix(redirect, ">") || strings.HasPrefix(redirect, "&>") {
			if strings.Trim(redirect, "<>&|") == "" {
				i++ // The file is the next word.
			}
			continue
		}
		word := strings.Trim(fields[i], "'\";()&|<>")
		if strings.HasPrefix(word, "-") {
			_, value, found := strings.Cut(word, "=")
			if !found {
				continue
			}
			word = strings.Trim(value, "'\"")
		}
		words = append(words, word)
	}
	return words
}

// tempLinePlain reports whether a command line leaves where the output of a
// command goes to what readStays sees: it holds no command substitution, $(
// or a backtick, and no process substitution, <( or >(, whose output can
// become a command or a file, and when it holds more than one command, no
// here-document, whose body the gate reads as commands, and no exec, eval,
// source or . command, which can point the output of every later command at
// a file or run a file. So x=$(cat $T/f); $x and exec > x.sh; cat $T/f do
// not count.
func tempLinePlain(command string) bool {
	if strings.Contains(command, "`") || strings.Contains(command, "$(") || strings.Contains(command, "<(") || strings.Contains(command, ">(") {
		return false
	}
	var parts []string
	for _, part := range rawExecParts(command) {
		if strings.TrimSpace(strings.ReplaceAll(part, "\\\n", "")) != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) <= 1 {
		return true
	}
	if strings.Contains(strings.ReplaceAll(command, "<<<", ""), "<<") {
		return false
	}
	return !slices.ContainsFunc(parts, runsInShell)
}

// shellRunners are the builtins that run a file or a string in the shell
// itself, or that point its output elsewhere for the commands after them.
var shellRunners = []string{"exec", "eval", "source", "."}

// runsInShell reports whether a command part, after keywords, braces,
// assignments and the command or builtin builtins, is one of shellRunners.
func runsInShell(part string) bool {
	words := strings.Fields(commandAfterKeywords(part))
	for len(words) > 0 {
		word := strings.TrimLeft(words[0], "{(!")
		switch {
		case word == "", execAssignment.MatchString(word):
		case word == "command" || word == "builtin":
			for len(words) > 1 && strings.HasPrefix(words[1], "-") {
				words = words[1:]
			}
		default:
			return slices.Contains(shellRunners, programName(word))
		}
		words = words[1:]
	}
	return false
}

// otherDescriptor finds an output redirect to a file descriptor other than
// standard output and standard error, as in >&3 or 1>&4, which an earlier
// redirect of the line may point at a file.
var otherDescriptor = regexp.MustCompile(`>&\s*([03-9]|[0-9]{2,})`)

// printPrograms only print what they read, when readOnlyCommand accepts
// their options.
var printPrograms = []string{"cat", "head", "tail", "wc", "grep", "egrep", "fgrep", "cut", "uniq", "nl", "sort"}

// readStays reports whether what a read or ls command part of a's line prints
// stays with the worker: every command that its output is piped into,
// directly or down the pipeline, only prints (printPrograms), and no output
// redirect of it or of them writes anything but a stream such as /dev/null or
// a file in the worker's own Claude session temp folder. So cat x | sh,
// ls x | sh and cat x > y.sh do not count, since a program could run what
// they pass on, and the worker names the files that ls lists. part is a raw
// part of the line (rawExecParts), blanks around it aside; a part that is no
// raw part, such as one of a masked line, never stays.
func readStays(a Action, part string) bool {
	found := false
	part = strings.TrimSpace(part)
	for _, pipeline := range pipelines(a.Target) {
		at := slices.IndexFunc(pipeline, func(p string) bool { return strings.TrimSpace(p) == part })
		if at < 0 {
			continue
		}
		found = true
		for i, next := range pipeline[at:] {
			if i > 0 && !printsOnly(next) || otherDescriptor.MatchString(next) {
				return false
			}
			if slices.ContainsFunc(redirectTargets(next), func(file string) bool {
				return !streamDevice(file) && !ownTempWord(a, file, tempRedirect)
			}) {
				return false
			}
		}
	}
	return found
}

// printsOnly reports whether a command part only prints what it reads.
func printsOnly(part string) bool {
	words := strings.Fields(commandAfterKeywords(part))
	return len(words) > 0 && slices.Contains(printPrograms, programName(words[0])) && readOnlyCommand(strings.Join(words, " "))
}

// pipelines groups the raw parts of a command line into pipelines: a part
// that follows a single | or |& reads the output of the part before it, also
// when blanks, line ends or a backslash before a line end stand between them,
// as in cat x |<newline>sh. A part that holds nothing, or only the & of |&,
// belongs to the separator before the next part.
func pipelines(command string) [][]string {
	var groups [][]string
	offset, gapStart := 0, 0
	for _, part := range rawExecParts(command) {
		at := offset + strings.Index(command[offset:], part)
		offset = at + len(part)
		if text := strings.TrimSpace(strings.ReplaceAll(part, "\\\n", "")); text == "" || text == "&" {
			continue
		}
		gap := strings.NewReplacer("\\\n", "", " ", "", "\t", "", "\n", "", "\r", "").Replace(command[gapStart:at])
		if (gap == "|" || gap == "|&") && len(groups) > 0 {
			groups[len(groups)-1] = append(groups[len(groups)-1], part)
		} else {
			groups = append(groups, []string{part})
		}
		gapStart = offset
	}
	return groups
}
