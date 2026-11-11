package gate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/core/policy"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/trust"
	"github.com/lorem-dev/duolog/internal/trusthome"
	"github.com/pelletier/go-toml/v2"
)

// ImportLead reads only the orchestrator's agent settings. Settings can narrow
// the registry's live mode, but cannot raise it or remove an inherited deny.
func ImportLead(root, agent string) (policy.LeadRules, error) {
	var rules policy.LeadRules
	if agent != "claude" && agent != "codex" {
		return rules, fmt.Errorf("unknown lead agent %q", agent)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return rules, err
	}
	var paths []string
	if agent == "claude" {
		paths = []string{filepath.Join(trusthome.Claude(home), "settings.json"), filepath.Join(root, ".claude/settings.json"), filepath.Join(root, ".claude/settings.local.json")}
	} else {
		paths = []string{filepath.Join(trust.CodexHome(home), "config.toml"), filepath.Join(root, ".codex/config.toml")}
	}
	for _, path := range inheritedLeadPaths(root, agent, paths) {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return rules, fmt.Errorf("%s: %w", path, err)
		}
		if agent == "claude" {
			var object map[string]json.RawMessage
			if err := json.Unmarshal(data, &object); err != nil || object == nil {
				return rules, fmt.Errorf("%s: expected settings object", path)
			}
			if permissions, ok := object["permissions"]; ok {
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(permissions, &fields); err != nil || fields == nil {
					return rules, fmt.Errorf("%s: expected permissions object", path)
				}
				if deny, ok := fields["deny"]; ok && strings.TrimSpace(string(deny)) == "null" {
					return rules, fmt.Errorf("%s: expected deny array", path)
				}
			}
			var settings struct {
				Permissions struct {
					Deny []string `json:"deny"`
				} `json:"permissions"`
			}
			if err := json.Unmarshal(data, &settings); err != nil {
				return rules, fmt.Errorf("%s: %w", path, err)
			}
			for _, entry := range settings.Permissions.Deny {
				rule, err := claudeDeny(entry)
				if err != nil {
					return rules, fmt.Errorf("%s: %w", path, err)
				}
				if (rule.Action == "read" || rule.Action == "write") && strings.Contains(entry, "(") {
					base := root
					if path == filepath.Join(trusthome.Claude(home), "settings.json") {
						base = filepath.Dir(path)
					}
					rule.Match, err = claudePathPattern(rule.Match, root, home, base)
					if err != nil {
						return rules, fmt.Errorf("%s: %w", path, err)
					}
				}
				rules.Deny = append(rules.Deny, rule)
			}
		} else {
			var settings struct {
				Sandbox  string `toml:"sandbox_mode"`
				Approval string `toml:"approval_policy"`
			}
			if err := toml.Unmarshal(data, &settings); err != nil {
				return rules, fmt.Errorf("%s: %w", path, err)
			}
			lower := func(m mode.Mode) {
				if rules.Mode == "" {
					rules.Mode = m
				} else {
					rules.Mode = mode.Lower(rules.Mode, m)
				}
			}
			switch settings.Sandbox {
			case "":
			case "read-only", "workspace-write", "danger-full-access":
				lower(mode.Parse("codex", settings.Sandbox))
			default:
				return rules, fmt.Errorf("%s: unknown sandbox_mode %q", path, settings.Sandbox)
			}
			switch settings.Approval {
			case "", "never":
			// Codex edits the project without asking under these; see session.CodexMode.
			case "on-request", "on-failure":
				lower(mode.Edits)
			case "untrusted":
				lower(mode.Ask)
			default:
				return rules, fmt.Errorf("%s: unknown approval_policy %q", path, settings.Approval)
			}
		}
	}
	return rules, nil
}

// ImportLeadForWork reads the ceiling of a worker in workRoot under a lead
// whose own checkout is leadRoot: the settings of both roots, so a worker in
// a duolog submodule never exceeds the lead in its superproject. Denies are
// united, the lower mode wins, and a path selector of the lead is read where
// the lead wrote it (leadDenyForWork). Either root's unreadable settings
// fail the import, and so does a Git failure while placing the work root
// against the lead's.
func ImportLeadForWork(workRoot, leadRoot, agent string) (policy.LeadRules, error) {
	return ImportLeadForWorkState(workRoot, "", leadRoot, agent)
}

// ImportLeadForWorkState is ImportLeadForWork for a caller that knows the
// main checkout of the work root, workState ("" when it does not), as the
// gate does: a work root and a lead root of one project are then placed
// without Git when the lead's is a main checkout.
func ImportLeadForWorkState(workRoot, workState, leadRoot, agent string) (policy.LeadRules, error) {
	rules, err := ImportLead(workRoot, agent)
	if err != nil {
		return policy.LeadRules{}, err
	}
	if leadRoot == "" || sameRoot(workRoot, leadRoot) {
		return rules, nil
	}
	lead, err := ImportLead(leadRoot, agent)
	if err != nil {
		return policy.LeadRules{}, fmt.Errorf("lead root %s: %w", leadRoot, err)
	}
	place, err := placeOfWork(leadRoot, workRoot, workState)
	if err != nil {
		return policy.LeadRules{}, fmt.Errorf("cannot place work root %s against lead root %s: %w", workRoot, leadRoot, err)
	}
	for _, rule := range lead.Deny {
		for _, r := range leadDenyForWork(rule, leadRoot, workRoot, place) {
			if !slices.ContainsFunc(rules.Deny, func(have config.Rule) bool { return sameRule(have, r) }) {
				rules.Deny = append(rules.Deny, r)
			}
		}
	}
	if rules.Mode == "" {
		rules.Mode = lead.Mode
	} else if lead.Mode != "" {
		rules.Mode = mode.Lower(rules.Mode, lead.Mode)
	}
	return rules, nil
}

// LeadRoot is the checkout a lead was recorded in. A lead recorded before
// roots were stored falls back to the main checkout above the registry.
func LeadRoot(lead session.Lead, reg *session.Registry) string {
	if lead.Root != "" {
		return lead.Root
	}
	return filepath.Dir(reg.Dir)
}

func sameRoot(a, b string) bool {
	return pathutil.Clean(pathutil.Resolved(a)) == pathutil.Clean(pathutil.Resolved(b))
}

func sameRule(a, b config.Rule) bool {
	return a.Action == b.Action && a.Match == b.Match && a.Decision == b.Decision &&
		a.ReadOnly == b.ReadOnly && a.Under == b.Under && slices.Equal(a.Roles, b.Roles)
}

// leadPlace is where a worker's project lies against its lead's: by Git
// relations (placeOfWork) and by the paths of the two roots (lexicalPlace).
type leadPlace struct {
	// anchor is the path of the work project inside the lead's project, "."
	// for the lead's project itself; found says that Git showed it.
	anchor string
	found  bool
	// above says that the lead's project lies inside the work project.
	above bool
	// inside is the path of the work root inside the lead root, "" when it
	// lies elsewhere; leadInside says that the lead root lies inside the work
	// root. Both compare the paths alone.
	inside     string
	leadInside bool
}

// maxSubmoduleDepth bounds the walk up through superprojects.
const maxSubmoduleDepth = 16

// placeOfWork places the work root against the lead root. workState is the
// work root's main checkout when the caller knows it, else "". A Git failure
// is an error, never taken for no relation.
func placeOfWork(leadRoot, workRoot, workState string) (leadPlace, error) {
	place := lexicalPlace(leadRoot, workRoot)
	leadState, err := project.CheckedStateRoot(leadRoot)
	if err == nil && workState == "" {
		workState, err = project.CheckedStateRoot(workRoot)
	}
	if err != nil {
		return place, err
	}
	if sameRoot(leadState, workState) {
		place.anchor, place.found = ".", true
		return place, nil
	}
	if place.anchor, place.found, err = submodulePath(workRoot, leadState); err != nil || place.found {
		return place, err
	}
	_, place.above, err = submodulePath(leadRoot, workState)
	return place, err
}

// lexicalPlace is where the work root and the lead root lie in each other by
// their paths alone, with no Git.
func lexicalPlace(leadRoot, workRoot string) leadPlace {
	var place leadPlace
	place.inside, _ = insidePath(leadRoot, workRoot)
	_, place.leadInside = insidePath(workRoot, leadRoot)
	return place
}

// insidePath is the slash path of inner below outer, when inner lies inside
// outer and is not outer itself; symlinks are resolved first.
func insidePath(outer, inner string) (string, bool) {
	rel, err := pathutil.Relative(pathutil.Resolved(outer), pathutil.Resolved(inner))
	if err != nil {
		return "", false
	}
	rel = pathutil.Clean(rel)
	if rel == "." || pathutil.Outside(rel) {
		return "", false
	}
	return pathutil.Slashes(rel), true
}

// submodulePath is the path of inner's project inside the project whose main
// checkout is top, found from inner's checkout up through the checkout that
// includes each project as a submodule (project.Superproject), until one is a
// checkout of top's project. Each step adds the submodule's path inside that
// checkout, so a submodule of a sibling linked checkout of top's project, and
// a linked worktree of a submodule, answer with the path a main checkout has.
// "." is top's project itself. A Git failure is an error.
func submodulePath(inner, top string) (string, bool, error) {
	var path []string
	checkout := inner
	for range maxSubmoduleDepth {
		main, err := project.CheckedStateRoot(checkout)
		if err != nil {
			return "", false, err
		}
		if sameRoot(main, top) {
			slices.Reverse(path)
			return pathutil.Clean(strings.Join(append([]string{"."}, path...), "/")), true, nil
		}
		parent, err := project.CheckedSuperproject(checkout)
		if err != nil || parent == "" {
			return "", false, err
		}
		rel, ok := insidePath(parent, main)
		if !ok {
			return "", false, nil
		}
		path = append(path, rel)
		checkout = parent
	}
	return "", false, nil
}

// leadDenyForWork gives the rules by which a read or write selector that
// ImportLead wrote relative to from bounds a worker in to. A whole-tool deny
// (**) holds everywhere and stays as it is. An absolute selector compares the
// absolute target, so it carries to as its Under.
//
// A relative selector whose place Git showed keeps its Match and carries the
// work project's path in the lead's project as its Under, so the policy reads
// it where the lead wrote it, with globs, braces and case as for any deny; its
// moved forms (movedDeny) are kept for the lead's files outside to, and all of
// them when from lies inside to, where they name the lead's files in the
// work. When the lead's project lies inside the work project, the moved forms
// name the lead's files. Otherwise the place is unknown, and the rule both
// stays as it is and gives every moved form. A work root inside the lead root
// also gets the rule anchored at its path there, Git relation or not.
func leadDenyForWork(rule config.Rule, from, to string, place leadPlace) []config.Rule {
	if rule.Action != "read" && rule.Action != "write" || rule.Match == "**" {
		return []config.Rule{rule}
	}
	if pathutil.IsAbs(rule.Match) {
		rule.Under = pathutil.Clean(to)
		return []config.Rule{rule}
	}
	rules := anchoredDeny(rule, place)
	for _, moved := range movedDeny(rule, from, to) {
		if !place.found || place.leadInside || pathutil.Outside(moved.Match) {
			rules = append(rules, moved)
		}
	}
	return rules
}

// anchoredDeny gives a relative selector read where the lead wrote it: at the
// work project's path that Git showed, as it is when the place is unknown,
// and at the work root's path inside the lead root.
func anchoredDeny(rule config.Rule, place leadPlace) []config.Rule {
	var rules []config.Rule
	if place.found {
		anchored := rule
		if place.anchor != "." {
			anchored.Under = place.anchor
		}
		rules = append(rules, anchored)
	} else if !place.above {
		rules = append(rules, rule)
	}
	if place.inside != "" {
		anchored := rule
		anchored.Under = place.inside
		rules = append(rules, anchored)
	}
	return rules
}

// movedDeny moves each brace alternative of a relative selector from from to
// to as a path: ../ for a file outside to, or, on another volume, the
// absolute path, which compares the absolute target.
func movedDeny(rule config.Rule, from, to string) []config.Rule {
	var rules []config.Rule
	for _, alternative := range policy.ExpandBraces(rule.Match) {
		moved := rule
		target := filepath.Join(from, filepath.FromSlash(alternative))
		if relative, err := pathutil.Relative(pathutil.Resolved(to), pathutil.Resolved(target)); err != nil {
			moved.Match, moved.Under = pathutil.Clean(target), pathutil.Clean(to)
		} else {
			moved.Match = pathutil.Clean(relative)
		}
		rules = append(rules, moved)
	}
	return rules
}

// Claude anchors // at the filesystem root, ~/ at home and / at the settings
// source (the project root for project/local settings, ~/.claude for user
// settings). Other paths use the project working directory. Hook targets use
// project-relative paths, including ../ for files outside the project.
func claudePathPattern(pattern, root, home, base string) (string, error) {
	var path string
	pattern = pathutil.Slashes(pattern)
	switch {
	case len(pattern) > 1 && pattern[1] == ':':
		path = pattern
	case len(pattern) >= 4 && pattern[0] == '/' && pattern[2] == ':' && pattern[3] == '/':
		// Native drive paths can carry Claude's extra absolute-path slash.
		path = filepath.FromSlash(pattern[1:])
	case strings.HasPrefix(pattern, "//"):
		path = filepath.FromSlash(strings.TrimPrefix(pattern, "/"))
		if runtime.GOOS == "windows" || pathutil.Windows(root) {
			rest := strings.TrimPrefix(pattern, "//")
			if len(rest) > 1 && rest[1] == '/' {
				path = filepath.FromSlash(rest[:1] + ":" + rest[1:])
			} else if len(rest) > 1 && rest[1] == ':' {
				path = filepath.FromSlash(rest)
			}
		}
	case strings.HasPrefix(pattern, "~/"):
		path = filepath.Join(home, filepath.FromSlash(strings.TrimPrefix(pattern, "~/")))
	case strings.HasPrefix(pattern, "/"):
		path = filepath.Join(base, filepath.FromSlash(strings.TrimPrefix(pattern, "/")))
	default:
		path = filepath.Join(root, filepath.FromSlash(pattern))
	}
	relative, err := pathutil.Relative(pathutil.Resolved(root), pathutil.Resolved(path))
	if err != nil {
		return pathutil.Clean(path), nil //nolint:nilerr // a path outside the root stays absolute
	}
	return pathutil.Clean(relative), nil
}

func claudeDeny(entry string) (config.Rule, error) {
	tool, pattern, hasPattern := strings.Cut(entry, "(")
	if hasPattern {
		if !strings.HasSuffix(pattern, ")") {
			return config.Rule{}, fmt.Errorf("malformed deny rule %q", entry)
		}
		pattern = strings.TrimSuffix(pattern, ")")
	} else {
		pattern = "**"
	}
	var kind string
	// A lead deny of mcp__server or mcp__server__tool holds for workers too.
	// mcp__my__wiki may name the server my__wiki, so it also covers every tool
	// whose full name starts with it.
	if server, ok := strings.CutPrefix(tool, "mcp__"); ok && !hasPattern {
		match := server + "/*"
		if target, ok := policy.MCPTarget(tool); ok {
			match = "{" + target + "," + target + "__*}"
		}
		rule := config.Rule{Action: "mcp", Match: match, Decision: policy.Deny}
		return rule, policy.ValidateRule(rule)
	}
	switch tool {
	case "Bash", "PowerShell":
		kind = "exec"
		// Claude's :* is a command-prefix rule, including the command alone.
		if prefix, ok := strings.CutSuffix(pattern, ":*"); ok {
			pattern = "{" + prefix + "," + prefix + " *}"
		}
	case "Read":
		kind = "read"
	case "Write", "Edit", "MultiEdit", "NotebookEdit":
		kind = "write"
	case "WebFetch", "WebSearch":
		kind = "net"
		pattern = strings.TrimPrefix(pattern, "domain:")
		if pattern != "**" {
			pattern = "{https://" + pattern + "/**,http://" + pattern + "/**," + pattern + "}"
		}
	case "Agent", "Task":
		kind = "spawn"
	default:
		if hasPattern {
			return config.Rule{}, fmt.Errorf("unsupported lead deny rule %q", entry)
		}
		kind, pattern = "unknown", tool
	}
	if pattern == "" {
		return config.Rule{}, fmt.Errorf("empty deny rule %q", entry)
	}
	rule := config.Rule{Action: kind, Match: strings.TrimPrefix(pattern, "./"), Decision: policy.Deny}
	return rule, policy.ValidateRule(rule)
}
