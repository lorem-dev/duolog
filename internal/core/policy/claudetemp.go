package policy

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// InClaudeTemp reports whether target, an absolute path as the worker wrote
// it, is one of temps or lies inside one once every symlink is resolved. A ..
// anywhere in target is refused before anything is cleaned, since after a
// symlink only the filesystem knows where it leads. So is a path that does not
// exist, whose symlinks cannot all be resolved. A folder counts only when no
// symlink below it leads out of its temp folder, since a recursive read such
// as grep -R or rg --follow follows one.
func InClaudeTemp(temps []string, target string) bool {
	if len(temps) == 0 || runtime.GOOS == "windows" || !filepath.IsAbs(target) {
		return false
	}
	if slices.Contains(strings.Split(target, "/"), "..") {
		return false
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return false
	}
	for _, temp := range temps {
		if temp != "" && within(temp, resolved) {
			return staysWithin(temp, resolved)
		}
	}
	return false
}

// within reports whether path is dir or lies inside it, with the separator
// and the case rules of the host.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

// maxTempEntries bounds the walk of staysWithin; a larger folder does not
// count as the worker's own.
const maxTempEntries = 10000

// staysWithin reports whether every symlink at or below path, a resolved path
// inside temp, resolves inside temp, and nothing else there is more than a
// folder or a regular file (specialType): a Windows junction, which Go reads
// as irregular, leads out as a symlink does.
func staysWithin(temp, path string) bool {
	entries := 0
	err := filepath.WalkDir(path, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entries++; entries > maxTempEntries {
			return fs.ErrInvalid
		}
		switch {
		case entry.Type() == fs.ModeSymlink:
			if resolved, err := filepath.EvalSymlinks(name); err != nil || !within(temp, resolved) {
				return fs.ErrPermission
			}
		case specialType(entry.Type()):
			return fs.ErrPermission
		}
		return nil
	})
	return err == nil
}

// tempUse is what a command does with a path in the worker's own Claude
// session temp folder; see ownTemp.
type tempUse int

const (
	// tempList: ls lists the folder's names, or names the file, and follows no
	// symlink below it.
	tempList tempUse = iota + 1
	// tempCreate: mkdir makes the folder, or finds it there.
	tempCreate
	// tempCopy: cp, which copies no symlink, writes the file as its
	// destination; it may not exist yet.
	tempCopy
	// tempRedirect: an output redirect writes the file, which may not exist
	// yet.
	tempRedirect
	// tempRead: a read command reads the file, or the folder and everything
	// below it; the file may not exist yet when the line redirects into it.
	tempRead
)

// ownTemp reports whether target, the absolute path that a command word names
// as the shell passes it on (plainWord), lies in the worker's own Claude
// session temp folder for the use given. A path with a character that the
// shell could expand or that quotes text, or with a .., never does, and
// neither does a path that leads out through a symlink, a dangling symlink,
// or a path in another session's folder. A path that does not exist yet
// counts for writes when its nearest existing folder resolves inside the temp
// folder, nothing on the way is a symlink, and no other command of the line
// can have made it or a folder above it first (madeInLine), since the gate
// sees the folder as it is before the line runs. A line that can turn what a
// command prints into code or send it elsewhere unseen never counts
// (tempLinePlain).
func ownTemp(a Action, target string, use tempUse) bool {
	if !tempCandidate(a, target) {
		return false
	}
	resolved, err := filepath.EvalSymlinks(target)
	switch {
	case use == tempRead && err == nil:
		return InClaudeTemp(a.Temp, target)
	case err == nil || use == tempList:
		return err == nil && existingInTemp(a.Temp, resolved, use)
	case use == tempRead && !redirectsInto(a.Target, target):
		// A file that does not exist and that no redirect of the line makes.
		return false
	}
	return newInTemp(a.Temp, target) && !madeInLine(a, target)
}

// tempCandidate reports whether ownTemp may count target at all: the worker
// has a temp folder, the line is a POSIX one that tempLinePlain accepts, and
// target is an absolute path with no .. and no character that the shell could
// expand or that quotes text.
func tempCandidate(a Action, target string) bool {
	if len(a.Temp) == 0 || runtime.GOOS == "windows" || !posixShell(a) || strings.ContainsAny(target, "\\$`*?[]{}~'\"\n") {
		return false
	}
	return tempLinePlain(a.Target) && filepath.IsAbs(target) && !slices.Contains(strings.Split(target, "/"), "..")
}

// existingInTemp is ownTemp for a path that exists, resolved: it lies in one
// of temps, and cp into a folder, which writes a file below it that may be a
// symlink out, needs every symlink below the folder to stay inside.
func existingInTemp(temps []string, resolved string, use tempUse) bool {
	temp := tempHolding(temps, resolved)
	if temp == "" {
		return false
	}
	if use == tempList || use == tempCreate {
		return true
	}
	info, err := os.Stat(resolved)
	return err == nil && (!info.IsDir() || staysWithin(temp, resolved))
}

// copiesNoLink reports whether cp's words, its program at words[0], copy
// file contents only: before --, each option is a cluster of -f, -i, -n, -p,
// -v and -L. -R, -r, -a, -P, -d, -s, -l and -t, and any long option, can copy
// or make a link, or name the destination elsewhere.
func copiesNoLink(words []string) bool {
	for _, word := range words[1:] {
		if word == "--" {
			return true
		}
		if strings.HasPrefix(word, "-") && (len(word) < 2 || strings.Trim(word[1:], "fipnvL") != "") {
			return false
		}
	}
	return true
}

// tempReadPrograms print what they read and run nothing, when readOnlyCommand
// accepts their options.
var tempReadPrograms = []string{"cat", "head", "tail", "wc", "grep", "egrep", "fgrep", "rg", "cut", "uniq", "nl", "sort", "stat", "du", "file"}

// operandTempUse returns what a command, its program at words[0], does with
// its operand at words[i] when that names a path in the worker's own Claude
// session temp folder (ownTemp), or 0 when the gate does not count it as the
// worker's own: ls lists it without -R, -L or a long option, which can follow
// a symlink below it; mkdir and touch make it; cp writes its last operand
// when it copies no link (copiesNoLink); a read command of tempReadPrograms
// reads it. Any other use, such as cp's source or a script that a program
// runs, is 0. The caller passes only a part's own program, never one behind
// a wrapper such as xargs, which can add operands after the last word.
func operandTempUse(words []string, i int) tempUse {
	options := words[1:]
	program := programName(words[0])
	switch program {
	case "mkdir", "touch":
		return tempCreate
	case "cp":
		if i != len(words)-1 || !copiesNoLink(words) {
			return 0
		}
		return tempCopy
	case "ls":
		if slices.ContainsFunc(options, func(w string) bool {
			return strings.HasPrefix(w, "--") || strings.HasPrefix(w, "-") && strings.ContainsAny(w, "RL")
		}) {
			return 0
		}
		return tempList
	}
	if slices.Contains(tempReadPrograms, program) && readOnlyCommand(strings.Join(words, " ")) {
		return tempRead
	}
	return 0
}

// tempOperand reports whether the operand at command[i], its text word as
// the shell passes it on, names a path in the worker's own Claude session
// temp folder for what the command, the part's own, does with it
// (operandTempUse). Before --, only the value of an option such as --file=x
// can name one. A read or a listing counts only when what it prints stays
// with the worker (readStays).
func tempOperand(a Action, part string, command []string, i int, word string, options bool) bool {
	if len(command) == 0 || i < 1 || i >= len(command) {
		return false
	}
	use := operandTempUse(command, i)
	if use == 0 || (use == tempRead || use == tempList) && !readStays(a, part) {
		return false
	}
	if options {
		word = pathWord(word)
	}
	return ownTemp(a, word, use)
}

// rawTempOperand is tempOperand for the operand at command[i] as written.
func rawTempOperand(a Action, part string, command []string, i int, options bool) bool {
	if i < 1 || i >= len(command) {
		return false
	}
	text, plain := plainWord(strings.Trim(command[i], "<>&|;()"))
	return plain && tempOperand(a, part, command, i, text, options)
}

// ownTempTarget reports whether a path that the read command words name, as
// written, is the worker's own in its Claude session temp folder beyond what
// ownTempRead finds: a folder that ls lists without following symlinks, when
// what each ls of the line that names it prints stays with the worker
// (listingStays), or a file that an output redirect of the line writes, which
// may not exist yet.
func ownTempTarget(a Action, words []string, written string) bool {
	if programName(words[0]) == "ls" && operandTempUse(words, len(words)-1) == tempList && listingStays(a, written) && ownTemp(a, written, tempList) {
		return true
	}
	return redirectsInto(a.Target, written) && ownTemp(a, written, tempRedirect)
}

// listingStays reports whether a's line has a raw part whose program is ls
// and that names written, the path as written, and what every such part
// prints stays with the worker (readStays): the worker names the files in
// its temp folder, so ls x | sh can run one of them.
func listingStays(a Action, written string) bool {
	written = strings.Trim(written, `'"`)
	found := false
	for _, part := range rawExecParts(a.Target) {
		words := strings.Fields(commandAfterKeywords(part))
		for len(words) > 0 && execAssignment.MatchString(words[0]) {
			words = words[1:]
		}
		if len(words) == 0 || programName(words[0]) != "ls" || !slices.Contains(lineOperands(part), written) {
			continue
		}
		if !readStays(a, part) {
			return false
		}
		found = true
	}
	return found
}

// ownTempWord is ownTemp for a command word as written.
func ownTempWord(a Action, word string, use tempUse) bool {
	text, ok := plainWord(word)
	return ok && ownTemp(a, text, use)
}

// redirectsInto reports whether an output redirect of a command line, or of
// one part of it, writes the file text, as the shell passes it on.
func redirectsInto(command, text string) bool {
	return slices.ContainsFunc(rawExecParts(command), func(part string) bool {
		return slices.ContainsFunc(redirectTargets(part), func(file string) bool {
			plain, ok := plainWord(file)
			return ok && plain == text
		})
	})
}

// plainWord returns the text of a command word as written that the shell
// passes on unchanged but for one pair of quotes around all of it, as in
// "/tmp/x" or '/tmp/x', and false for any other quoting.
func plainWord(word string) (string, bool) {
	if len(word) >= 2 && (word[0] == '"' || word[0] == '\'') && word[len(word)-1] == word[0] {
		word = word[1 : len(word)-1]
	}
	return word, !strings.ContainsAny(word, "'\"\\")
}

// newInTemp reports whether target, an absolute path without .., does not
// exist, with no symlink at its place or above it up to its nearest existing
// folder, and that folder resolves inside one of temps.
func newInTemp(temps []string, target string) bool {
	for name := target; ; {
		if _, err := os.Lstat(name); !errors.Is(err, fs.ErrNotExist) {
			// A dangling symlink, or a name the gate cannot read.
			return false
		}
		parent := filepath.Dir(name)
		if parent == name {
			return false
		}
		if _, err := os.Lstat(parent); errors.Is(err, fs.ErrNotExist) {
			name = parent
			continue
		}
		resolved, err := filepath.EvalSymlinks(parent)
		if err != nil || tempHolding(temps, resolved) == "" {
			return false
		}
		folder, err := os.Stat(resolved)
		return err == nil && folder.IsDir()
	}
}

// tempHolding returns the folder of temps that holds resolved, a path with
// its symlinks resolved, or "".
func tempHolding(temps []string, resolved string) string {
	for _, temp := range temps {
		if temp != "" && within(temp, resolved) {
			return temp
		}
	}
	return ""
}

// ownTempRead reports whether a file that a named read command in a reads, as
// written in the command, lies in the worker's own Claude session temp folder.
func ownTempRead(a Action, target string) bool {
	if len(a.Temp) == 0 {
		return false
	}
	if !filepath.IsAbs(target) {
		cwd := a.Cwd
		if cwd == "" {
			cwd = a.Work
		}
		if cwd == "" {
			return false
		}
		// A plain join keeps every .. for InClaudeTemp to refuse.
		target = cwd + "/" + target
	}
	return InClaudeTemp(a.Temp, target)
}
