package policy

import (
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
)

// RuleTaskRead allows a worker to read a path outside the work root that its
// task lists under Read.
const RuleTaskRead = "task read"

// TaskReadsEnv is the variable through which delegate hands its worker's gate
// the absolute paths, folders and globs that the task lists under Read. The
// worker cannot set it: duolog's own names never pass on from the lead.
const TaskReadsEnv = "DUOLOG_READ"

// TaskReadsValue is the value of TaskReadsEnv for the paths of a task.
func TaskReadsValue(paths []string) string {
	data, _ := json.Marshal(paths)
	return string(data)
}

// TaskReads reads the value of TaskReadsEnv: the absolute entries of the
// list, or none when the value cannot be read.
func TaskReads(value string) []string {
	var paths []string
	if json.Unmarshal([]byte(value), &paths) != nil {
		return nil
	}
	return slices.DeleteFunc(paths, func(p string) bool { return !filepath.IsAbs(pathutil.Native(p)) })
}

// outsideReadAllow allows a read outside the work root that is the worker's
// own, as OwnResult says, an agent skill, or one its task lists under Read,
// unless the read rules asked about the read. Every deny was judged before.
func outsideReadAllow(p config.Policy, lead LeadRules, a Action, readAsk *Decision, decision func(kind, reason, rule string) Decision) (Decision, bool) {
	switch {
	case a.Kind != "read" || !outside(a.Target):
		return Decision{}, false
	case a.OwnResult:
		return decision(Allow, "the worker's own saved tool result or session temp file", "own tool results"), true
	case readAsk == nil && (a.agentRead || agentReadTool(a, searchable(p, lead, a))):
		return decision(Allow, "the agent's own skill files", "agent skills"), true
	case readAsk == nil && (a.taskRead || taskReadTool(a, searchable(p, lead, a))):
		return decision(Allow, "a path the task lists under Read", RuleTaskRead), true
	}
	return Decision{}, false
}

// searchable returns the check that a folder, resolved, may be searched,
// listed or globbed as a path the task lists: a walk of at most
// maxTempEntries entries below it, all that a search from it can reach,
// finds no symlink, which rg -L or grep -R would follow anywhere, even to a
// .env beside the folder; no hidden name, which covers .duolog, .env and
// other hidden credential files that Claude's Grep reads with --hidden; no
// name shaped like an 8.3 short name, which can stand for one; and no path
// that the state rule or a read deny of the lead or the policy refuses. No
// private store may lie in it either.
func searchable(p config.Policy, lead LeadRules, a Action) func(dir string) bool {
	return func(dir string) bool {
		base := dir
		if a.Work != "" {
			base = RelativeTarget(a.Work, a.Work, dir)
		}
		denies := denyRules(p, lead)
		entries := 0
		err := filepath.WalkDir(dir, func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entries++; entries > maxTempEntries {
				return fs.ErrInvalid
			}
			rel, err := filepath.Rel(dir, name)
			if err != nil || rel == "." {
				return err
			}
			target := cleanPath(path.Join(pathutil.Slashes(base), filepath.ToSlash(rel)))
			if specialType(entry.Type()) || strings.HasPrefix(entry.Name(), ".") || shortNamePath(filepath.ToSlash(rel)) || StateRead(target) || deniedRead(denies, a, target) {
				return fs.ErrPermission
			}
			return nil
		})
		return err == nil && !slices.ContainsFunc(closedRoots(a), func(root string) bool { return within(dir, pathutil.Resolved(root)) })
	}
}

// specialType reports whether the type bits of a walked entry name anything
// but a folder or a regular file: a symlink, a Windows junction, which Go
// reads as irregular since 1.23 and which a search follows like a symlink, a
// socket, a pipe or a device.
func specialType(mode fs.FileMode) bool {
	return mode&fs.ModeType&^fs.ModeDir != 0
}

// deniedRead reports whether a read deny among denies refuses target.
func deniedRead(denies []config.Rule, a Action, target string) bool {
	return slices.ContainsFunc(denies, func(rule config.Rule) bool {
		return rule.Action == "read" && matches(rule, Action{Kind: "read", Target: target, Role: a.Role, Shell: a.Shell})
	})
}

// taskReadTool reports whether a read tool's target, which the hook placed
// from the resolved work root, is one the task lists under Read; searchable
// judges a folder or the fixed folder of a glob.
func taskReadTool(a Action, searchable func(string) bool) bool {
	if len(a.Reads) == 0 || a.Work == "" || strings.HasPrefix(a.Target, "~") {
		return false
	}
	target := pathutil.Native(a.Target)
	if !filepath.IsAbs(target) {
		target = filepath.Join(pathutil.Resolved(pathutil.Native(a.Work)), target)
	}
	return taskReadPath(a.Reads, target, searchable)
}

// taskReadWord reports whether a path that a named read command reads, as
// written in the command, is one the task lists under Read. A plain join
// keeps every .. for taskReadPath to refuse.
func taskReadWord(a Action, written string, searchable func(string) bool) bool {
	if len(a.Reads) == 0 {
		return false
	}
	target := expandHome(written)
	if !filepath.IsAbs(pathutil.Native(target)) {
		cwd := a.Cwd
		if cwd == "" {
			cwd = a.Work
		}
		if cwd == "" {
			return false
		}
		target = cwd + "/" + target
	}
	return taskReadPath(a.Reads, target, searchable)
}

// taskReadPath reports whether target, an absolute path, is covered by one of
// reads once every symlink is resolved: it is a listed file, it lies in a
// listed folder, or it is a file that a listed glob matches. A .. anywhere in
// target is refused first, since after a symlink only the filesystem knows
// where it leads, and so is a path that does not exist. A folder counts only
// when no symlink below it leads out of the listed folder, since a recursive
// read can follow one, and only when searchable passes it. A glob, as the
// Glob tool sends, counts when its fixed folder lies in a listed folder, or
// when it is a listed glob itself, and searchable passes its fixed folder.
func taskReadPath(reads []string, target string, searchable func(string) bool) bool {
	if slices.Contains(strings.Split(pathutil.Slashes(target), "/"), "..") || runtime.GOOS != "windows" && strings.Contains(target, `\`) {
		return false
	}
	native := pathutil.Native(target)
	if !filepath.IsAbs(native) {
		return false
	}
	if glob(native) {
		fixed, err := filepath.EvalSymlinks(ReadEntryFolder(native))
		return err == nil && slices.ContainsFunc(reads, func(entry string) bool { return globCovered(entry, native) }) && searchable(fixed)
	}
	resolved, err := filepath.EvalSymlinks(native)
	if err != nil {
		return false
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return false
	}
	covered := slices.ContainsFunc(reads, func(entry string) bool { return entryCovers(entry, resolved, info.IsDir()) })
	return covered && (!info.IsDir() || searchable(resolved))
}

// glob reports whether a path holds a pattern character.
func glob(path string) bool { return strings.ContainsAny(path, "*?[{") }

// entryCovers reports whether one Read entry covers resolved, an existing
// path with every symlink resolved.
func entryCovers(entry, resolved string, dir bool) bool {
	entry = pathutil.Native(entry)
	if !glob(entry) {
		root, err := filepath.EvalSymlinks(entry)
		return err == nil && within(root, resolved) && staysWithin(root, resolved)
	}
	if dir {
		return false
	}
	rel, ok := globRel(entry, resolved)
	return ok && globMatch(strings.TrimLeft(entry[len(ReadEntryFolder(entry)):], `/\`), rel)
}

// globCovered reports whether one Read entry covers target, a glob: its
// fixed folder lies in the folder entry, with no symlink below it leading
// out, or it is the glob entry itself.
func globCovered(entry, target string) bool {
	entry = pathutil.Native(entry)
	fixed, err := filepath.EvalSymlinks(ReadEntryFolder(target))
	if err != nil {
		return false
	}
	if !glob(entry) {
		root, err := filepath.EvalSymlinks(entry)
		return err == nil && within(root, fixed) && staysWithin(root, fixed)
	}
	root, err := filepath.EvalSymlinks(ReadEntryFolder(entry))
	if err != nil || !samePath(root, fixed) {
		return false
	}
	return samePath(entry[len(ReadEntryFolder(entry)):], target[len(ReadEntryFolder(target)):])
}

// globRel resolves the fixed folder of a glob entry and returns resolved
// relative to it; ok is false when resolved does not lie in it.
func globRel(entry, resolved string) (rel string, ok bool) {
	root, err := filepath.EvalSymlinks(ReadEntryFolder(entry))
	if err != nil || !within(root, resolved) {
		return "", false
	}
	rel, err = filepath.Rel(root, resolved)
	return rel, err == nil
}

// ReadEntryFolder is the fixed folder of a Read entry: the folder of a glob
// before its first segment with a pattern character, or the entry itself.
func ReadEntryFolder(name string) string {
	end := strings.IndexAny(name, "*?[{")
	if end < 0 {
		return name
	}
	return filepath.Dir(name[:end+1])
}

// globMatch matches a relative path against a relative pattern, without case
// on Windows.
func globMatch(pattern, rel string) bool {
	pattern, rel = filepath.ToSlash(pattern), filepath.ToSlash(rel)
	if runtime.GOOS == "windows" {
		pattern, rel = strings.ToLower(pattern), strings.ToLower(rel)
	}
	ok, err := doublestar.Match(pattern, rel)
	return err == nil && ok
}

// samePath compares two spellings of a path, without case on Windows.
func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
