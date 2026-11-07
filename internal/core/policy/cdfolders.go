package policy

import (
	"path/filepath"
	"slices"
	"strings"
)

// maxCdFolders bounds the folders cdFolders follows; a line whose cds can lead
// to more is outside, so the person decides.
const maxCdFolders = 16

// cdFolders returns the folders other than the one the command starts in that
// the cd and pushd commands of a POSIX line can lead to, each one taken or
// failed in turn. A folder is absolute. ok is false when there are more
// than maxCdFolders. A cd whose folder the gate cannot read, such as cd - or
// cd "$d", adds none: its words are judged as operands already.
func cdFolders(a Action) (folders []string, ok bool) {
	start := a.Cwd
	if start == "" {
		start = a.Work
	}
	if start == "" {
		return nil, true
	}
	reached := []string{start}
	for _, part := range rawExecParts(a.Target) {
		folder, found := cdFolder(part)
		if !found {
			continue
		}
		for _, from := range reached {
			// cd reads .. from the folder's name; cd -P, and a relative path
			// after any cd, from the real folder that a symlink leads to. The
			// gate takes both.
			logical := folder
			if !filepath.IsAbs(logical) {
				logical = filepath.Join(from, logical)
			}
			for _, next := range []string{filepath.Clean(logical), resolvedTarget(physicalJoin(from, folder))} {
				if !slices.Contains(reached, next) {
					reached = append(reached, next)
				}
			}
		}
		if len(reached) > maxCdFolders+1 {
			return nil, false
		}
	}
	return reached[1:], true
}

// cdFolder returns the folder that a part changes to when it is a cd or pushd
// whose folder the gate can read, with ~ expanded: the home folder for a cd
// alone.
func cdFolder(part string) (string, bool) {
	words := strings.Fields(commandAfterKeywords(part))
	if len(words) == 0 || words[0] != "cd" && words[0] != "pushd" {
		return "", false
	}
	folder := ""
	if i := slices.IndexFunc(words[1:], func(word string) bool { return !strings.HasPrefix(word, "-") || word == "-" }); i >= 0 {
		folder = words[i+1]
	}
	switch {
	case folder == "" && words[0] == "cd":
		folder = "~"
	case folder == "" || folder == "-" || strings.ContainsAny(folder, "$`\\*?[{"):
		return "", false
	}
	return expandHome(unquoted.Replace(strings.TrimRight(folder, ";&|)"))), true
}
