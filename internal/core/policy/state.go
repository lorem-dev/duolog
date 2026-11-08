package policy

import (
	"os"
	"path"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/pathutil"
)

const RuleState = "duolog state"

// RuleReport names the allow for a peer writing its own report file.
const RuleReport = "peer report"

// ReportPath is the path, relative to the work root, where a peer hands back
// its report.
func ReportPath(peer string) string {
	return ".duolog/tasks/" + peer + "/report.md"
}

// ReportTarget reports whether target is the report file of this peer. The peer
// ID must be a plain name, so that no other peer or path can match.
func ReportTarget(peer, target string) bool {
	if peer == "" || strings.ContainsAny(peer, `/\*?[]{}.~$`) {
		return false
	}
	return target == ReportPath(peer)
}

func stateReadAction(a Action) bool {
	if StateRead(a.Target) || stateFile(a.Work, a.Work, a.Target) == stateOther {
		return true
	}
	// Read-tool targets have already been made relative to Work by the hook.
	// A search of one plain file that the state rule allows reads only it.
	if a.Search && a.Glob == "" && stateSearchFile(a.Work, a.Work, a.Target) {
		return false
	}
	return a.Search && (stateRoot(a.Work, a.Work, a.Target) ||
		stateOverride([]string{a.Glob}) && coversState(a.Work, a.Work, a.State, a.Target))
}

// StateRead reports whether a target names duolog state that workers cannot read.
func StateRead(target string) bool {
	rest, ok := stateRest(target)
	if !ok || rest == "" {
		return false
	}
	if ok, _ := path.Match("*.md", rest[strings.LastIndex(rest, "/")+1:]); ok {
		return false
	}
	for _, pattern := range []string{"config.yaml", "config.local.yaml", "learned.yaml", "skill-tests/*.plan.yaml"} {
		if ok, _ := path.Match(pattern, rest); ok {
			return false
		}
	}
	return true
}

// stateEntry says what a path inside a .duolog folder is on disk, judged from
// that folder down: missing, a plain file, or something else, which is a
// symlink, a Windows junction, a folder, a socket, a device or a path
// written with .. or a glob.
type stateEntry int

const (
	stateMissing stateEntry = iota
	statePlain
	stateOther
)

// stateFile looks up target, a path that a worker names, inside the first
// .duolog folder on it: the folder itself and every part below it must be no
// symlink, so the path cannot resolve outside the folder, and the last part
// must be a regular file. Parts above the folder, such as macOS's /tmp, may
// be symlinks. A target that names no path inside a .duolog folder, or that
// cannot be placed because work is unknown, is missing.
func stateFile(work, cwd, target string) stateEntry {
	if work == "" {
		return stateMissing
	}
	if cwd == "" {
		cwd = work
	}
	name := pathutil.Slashes(expandHome(target))
	if !pathutil.IsAbs(name) {
		name = pathutil.Slashes(cwd) + "/" + name
	}
	// A path that leaves the folder again, as .duolog/../src does, names no
	// path inside it; one that stays inside is refused when it holds a .., as
	// a symlink before the .. would lead elsewhere than the clean spelling.
	clean := pathutil.Clean(name)
	parts := strings.Split(clean, "/")
	at := slices.IndexFunc(parts, func(part string) bool { return strings.EqualFold(part, ".duolog") })
	if at < 0 || at == len(parts)-1 || strings.HasSuffix(name, "/") {
		return stateMissing
	}
	if slices.Contains(strings.Split(name, "/"), "..") || strings.ContainsAny(clean, "*?[{") {
		return stateOther
	}
	return stateWalk(parts, at)
}

// stateWalk looks up each part of a clean path from the part at index at
// down, without following a symlink: every part but the last must be a
// folder, and the last a regular file.
func stateWalk(parts []string, at int) stateEntry {
	for i := at; i < len(parts); i++ {
		info, err := os.Lstat(pathutil.Native(strings.Join(parts[:i+1], "/")))
		if err != nil {
			return stateMissing
		}
		last := i == len(parts)-1
		if last && !info.Mode().IsRegular() || !last && (!info.IsDir() || specialType(info.Mode())) {
			return stateOther
		}
	}
	return statePlain
}

// stateSearchFile reports whether a search names one plain file inside a
// .duolog folder that the state rule lets a worker read. Such a search reads
// that file and nothing else, so it is judged like the Read tool or cat, by
// StateRead, not as a search rooted in the state folder.
func stateSearchFile(work, cwd, target string) bool {
	return stateFile(work, cwd, target) == statePlain && !StateRead(target)
}

func stateRest(target string) (string, bool) {
	parts := strings.Split(pathutil.Slashes(target), "/")
	for i, part := range parts {
		if strings.EqualFold(part, ".duolog") {
			return strings.Join(parts[i+1:], "/"), true
		}
	}
	return "", false
}

func stateRoot(work, cwd, target string) bool {
	rel := RelativeTarget(work, cwd, expandHome(target))
	if !outside(rel) {
		return statePattern(rel)
	}
	if cwd == "" {
		cwd = work
	}
	target = expandHome(target)
	root := pathutil.Slashes(target)
	if !pathutil.IsAbs(root) {
		root = pathutil.Slashes(cwd) + "/" + root
	}
	if statePattern(pathutil.Clean(root)) {
		return true
	}
	return statePattern(rel)
}

// statePattern includes glob and brace spellings that can name a state segment.
func statePattern(pattern string) bool {
	for _, expanded := range expandBraces(strings.ToLower(pathutil.Slashes(pattern))) {
		for _, segment := range strings.Split(expanded, "/") {
			if !strings.HasPrefix(segment, ".") {
				continue
			}
			if ok, _ := path.Match(segment, ".duolog"); ok {
				return true
			}
		}
	}
	return false
}

func stateOverride(globs []string) bool {
	for _, glob := range globs {
		if strings.HasPrefix(glob, "!") {
			continue
		}
		// Override globs match leading dots, unlike shell-glob operands.
		for _, expanded := range expandBraces(strings.ToLower(pathutil.Slashes(glob))) {
			if ok, _ := path.Match(globLastSegment(expanded), ".duolog"); ok {
				return true
			}
		}
	}
	return false
}

// coversState reports whether a search root contains the project's state folder
// or names a path at or inside any state folder.
func coversState(work, cwd, state, target string) bool {
	if state == "" {
		return false
	}
	if stateRoot(work, cwd, target) {
		return true
	}
	root := RelativeTarget(work, cwd, expandHome(target))
	folder := RelativeTarget(work, work, pathutil.Slashes(state)+"/.duolog")
	rel, err := pathutil.Relative(root, folder)
	return err == nil && !pathutil.Outside(rel)
}

// recursiveStateRead only denies; uncertain syntax must not turn state reads
// into asks, and plain rg must still skip hidden state when searching a parent.
func recursiveStateRead(a Action, parts []string, uncertain bool) bool {
	if a.State == "" {
		return false
	}
	for _, part := range parts {
		if !readOnlyCommand(part) {
			continue
		}
		words := commandWords(part)
		name := strings.ToLower(words[0])
		recursive := recursiveSearch(name, words[1:])
		override := name == "rg" && stateOverride(rgGlobs(words[1:]))
		if !recursive && name != "rg" {
			continue
		}
		targets, _ := readCommandTargets(part)
		if override && len(targets) == 0 {
			targets = []string{"."}
		}
		if recursive && (uncertain || len(targets) == 0) && coversState(a.Work, a.Cwd, a.State, ".") {
			return true
		}
		if slices.ContainsFunc(targets, func(target string) bool { return stateSearchRoot(a, target, recursive, override) }) {
			return true
		}
	}
	return false
}

// stateSearchRoot reports whether a search from target can reach state. One
// plain file that the state rule allows is judged as a file by
// readTargetDecision, as cat and the Read tool are.
func stateSearchRoot(a Action, target string, recursive, override bool) bool {
	if !override && stateSearchFile(a.Work, a.Cwd, target) {
		return false
	}
	return stateRoot(a.Work, a.Cwd, target) || (recursive || override) && coversState(a.Work, a.Cwd, a.State, target)
}
