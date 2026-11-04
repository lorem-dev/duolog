package gate

import (
	"path"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/pathutil"
	"github.com/lorem-dev/duolog/internal/session"
)

const RuleState = "duolog state"

func stateReadAction(a Action) bool {
	if StateRead(a.Target) {
		return true
	}
	// Read-tool targets have already been made relative to Work by the hook.
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
		for _, target := range targets {
			if stateRoot(a.Work, a.Cwd, target) || (recursive || override) && coversState(a.Work, a.Cwd, a.State, target) {
				return true
			}
		}
	}
	return false
}

// LeadMode reads permissions at decision time, keeping the user's explicit
// lower ceiling. Claude's mode is recorded from each lead hook's stdin.
func LeadMode(reg *session.Registry, id string, started time.Time) (session.Lead, mode.Mode, error) {
	lead, effective, err := reg.EffectiveMode(id)
	if err != nil {
		return lead, effective, err
	}
	if lead.TranscriptPath != "" && lead.Agent == "codex" {
		observed, _ := session.ReadLeadState(lead.Agent, lead.TranscriptPath)
		live := observed.Mode
		if live == "" {
			live = mode.Lower(lead.Mode, mode.Ask)
		}
		// EffectiveMode includes the last observed mode; recompute against only the
		// explicit ceiling after refreshing the live observation.
		lead.Mode = live
		if _, err = reg.ObserveLead(session.LeadObservation{ID: lead.ID, Agent: lead.Agent, Mode: &lead.Mode}, started); err != nil {
			return lead, "", err
		}
		return reg.EffectiveMode(id)
	}
	return lead, effective, nil
}
