// Package gate decides worker permissions without a model call.
package gate

import (
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
)

// Ask is an unresolved policy result; hook callers resolve it with Escalate.
const (
	Allow = "allow"
	Deny  = "deny"
	Ask   = "ask"
)

type Action struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Role   string `json:"role"`
}

type Decision struct {
	Kind   string    `json:"decision"`
	Reason string    `json:"reason"`
	Rule   string    `json:"rule"`
	Mode   mode.Mode `json:"mode"`
}

// Evaluate applies denies before role and mode limits. Lead restrictions always
// apply: inherit_lead=false cannot grant access that the lead does not have.
func Evaluate(p config.Policy, lead LeadRules, m mode.Mode, a Action) Decision {
	if lead.Mode != "" {
		m = mode.Lower(m, lead.Mode)
	}
	decision := func(kind, reason, rule string) Decision { return Decision{kind, reason, rule, m} }
	for _, rule := range p.Rules {
		if err := validateRule(rule); err != nil {
			return decision(Deny, err.Error(), "invalid policy")
		}
	}
	for _, rule := range lead.Deny {
		if err := validateRule(rule); err != nil {
			return decision(Deny, err.Error(), "invalid lead policy")
		}
	}
	if (a.Kind == "read" || a.Kind == "write") && a.Target != "" {
		a.Target = cleanPath(a.Target)
	}
	if a.Kind == "spawn" {
		return decision(Deny, "workers cannot delegate", "worker depth")
	}
	parts := []string{a.Target}
	uncertain := false
	if a.Kind == "exec" {
		parts, uncertain = shellParts(a.Target)
	}
	// Check every part for a deny before any ask or allow can return.
	for _, part := range parts {
		partAction := a
		partAction.Target = part
		baseAction := partAction
		if a.Kind == "exec" {
			baseAction.Target = denyCommand(part)
		}
		// Preserve path-specific denies as well as basename command denies.
		for _, rule := range lead.Deny {
			if matches(rule, partAction) || matches(rule, baseAction) {
				return decision(Deny, "denied by lead settings", "lead: "+rule.Action+" "+rule.Match)
			}
		}
		for _, rule := range p.Rules {
			if rule.Decision == Deny && (matches(rule, partAction) || matches(rule, baseAction)) {
				return decision(Deny, "denied by project policy", rule.Action+" "+rule.Match)
			}
		}
	}

	if a.Kind == "internal" || a.Kind == "answer" {
		return decision(Allow, "returns the worker answer", "answer tool")
	}

	if a.Kind != "read" && a.Kind != "write" && a.Kind != "exec" && a.Kind != "net" {
		return decision(Ask, "unknown tool requires approval", "unknown tool")
	}
	if a.Kind == "write" {
		if lead.RoleSandbox == "read-only" || !roleCanWrite(p, a) {
			return decision(Deny, "role "+a.Role+" cannot write", "role sandbox")
		}
	}
	if a.Kind == "exec" && (lead.RoleSandbox == "read-only" || !roleCanWrite(p, Action{Kind: "write", Target: "role-check", Role: a.Role})) {
		if uncertain || len(parts) == 0 {
			return decision(Deny, "read-only role requires a named read-only command", "role sandbox")
		}
		for _, part := range parts {
			allowed := false
			for _, rule := range p.Rules {
				if rule.Decision == Allow && readOnlyRule(rule.Match) && readOnlyCommand(part) && matches(rule, Action{Kind: "exec", Target: part, Role: a.Role}) {
					allowed = true
					break
				}
			}
			if !allowed {
				return decision(Deny, "read-only role requires a named read-only command", "role sandbox")
			}
		}
	}
	if (a.Kind == "read" || a.Kind == "write") && outside(a.Target) {
		for _, rule := range p.Rules {
			if rule.Decision != Deny && matchesExplicitOutside(rule, a) {
				if a.Kind == "write" && m == mode.Plan {
					return decision(Deny, "plan mode permits only reads", "mode plan")
				}
				if a.Kind == "write" && m == mode.Ask {
					return decision(Ask, "ask mode requires approval for writes", "mode ask")
				}
				return decision(rule.Decision, "explicit outside path rule", rule.Action+" "+rule.Match)
			}
		}
		return decision(Ask, "path outside work root requires approval", "outside work root")
	}
	// apply_patch and other writes without a verified target cannot be allowed,
	// including in full mode.
	if a.Target == "" || uncertain {
		return decision(Ask, "tool target or shell syntax requires approval", "unverified action")
	}
	if m == mode.Plan && a.Kind != "read" {
		safe := a.Kind == "exec"
		for _, part := range parts {
			safe = safe && allowedReadCommand(p, a.Role, part)
		}
		if !safe {
			return decision(Deny, "plan mode permits only reads", "mode plan")
		}
	}
	if a.Kind == "write" && m == mode.Ask {
		return decision(Ask, "ask mode requires approval for writes", "mode ask")
	}
	result := decision(Allow, "allowed by policy", "policy")
	for _, part := range parts {
		partAction := a
		partAction.Target = part

		d := decision(p.OnUnmatched, "no matching policy rule", "on_unmatched")
		for _, rule := range p.Rules {
			if rule.Decision != Deny && matches(rule, partAction) {
				d = decision(rule.Decision, "matched project policy", rule.Action+" "+rule.Match)
				break
			}
		}
		if d.Kind != Allow && d.Kind != Ask && d.Kind != Deny {
			d = decision(Deny, "invalid policy decision", "invalid policy")
		}
		if m == mode.Full && d.Kind == Ask {
			d = decision(Allow, "full mode confirms action", "mode full: "+d.Rule)
		}
		if d.Kind == Deny {
			return d
		}
		if result.Kind != Ask {
			result = d
		}
	}
	if result.Kind == Ask {
		result.Reason += " (ask)"
	}
	return result
}

func roleCanWrite(p config.Policy, a Action) bool {
	scoped := false
	for _, r := range p.Rules {
		if r.Action == "write" && r.Decision == Allow {
			if len(r.Roles) > 0 {
				scoped = true
			}
			if matches(r, a) {
				return true
			}
		}
	}
	return !scoped
}

func cleanPath(path string) string {
	return filepath.ToSlash(filepath.Clean(strings.ReplaceAll(path, `\`, "/")))
}

func matches(r config.Rule, a Action) bool {
	if r.Action != a.Kind || len(r.Roles) > 0 && !slices.Contains(r.Roles, a.Role) {
		return false
	}
	for _, pattern := range expandBraces(r.Match) {
		if a.Kind == "read" || a.Kind == "write" {
			pattern = strings.TrimPrefix(pattern, "./")
			if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
				pattern = strings.ToLower(pattern)
				a.Target = strings.ToLower(a.Target)
			}
			ok, err := doublestar.Match(pattern, a.Target)
			if err == nil && ok {
				return true
			}
		} else if starMatch(pattern, a.Target) {
			return true
		}
	}
	return false
}

func validateRule(r config.Rule) error {
	if r.Match == "" {
		return fmt.Errorf("empty %s permission pattern", r.Action)
	}
	switch r.Action {
	case "read", "write":
		for _, pattern := range expandBraces(r.Match) {
			if !doublestar.ValidatePattern(pattern) {
				return fmt.Errorf("invalid %s permission pattern %q", r.Action, r.Match)
			}
		}
	case "exec", "net", "spawn", "unknown", "internal", "answer":
	default:
		return fmt.Errorf("unknown permission action %q", r.Action)
	}
	return nil
}

// Exec globs deliberately treat '/' as an ordinary character.
func starMatch(pattern, value string) bool {
	p, v, star, retry := 0, 0, -1, 0
	for v < len(value) {
		if p < len(pattern) && pattern[p] == '*' {
			star, retry = p, v
			p++
		} else if p < len(pattern) && pattern[p] == value[v] {
			p++
			v++
		} else if star >= 0 {
			retry++
			v, p = retry, star+1
		} else {
			return false
		}
	}
	return strings.Trim(pattern[p:], "*") == ""
}

func expandBraces(pattern string) []string {
	start, depth := -1, 0
	for i, c := range pattern {
		switch c {
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			depth--
			if depth != 0 || start < 0 {
				continue
			}
			var options []string
			last, nested := start+1, 0
			for j := start + 1; j < i; j++ {
				switch pattern[j] {
				case '{':
					nested++
				case '}':
					nested--
				case ',':
					if nested == 0 {
						options = append(options, pattern[last:j])
						last = j + 1
					}
				}
			}
			options = append(options, pattern[last:i])
			var result []string
			for _, option := range options {
				result = append(result, expandBraces(pattern[:start]+option+pattern[i+1:])...)
			}
			return result
		}
	}
	return []string{pattern}
}

// basenameCommand receives a verified shell part with normalized whitespace.
func basenameCommand(command string) string {
	word, _, _ := strings.Cut(command, " ")
	return filepath.Base(word) + command[len(word):]
}

// shellParts is deliberately conservative, not a shell interpreter. Quoted
// separators remain arguments; substitutions and unsupported syntax need review.
func shellParts(command string) ([]string, bool) {
	var parts []string
	start := 0
	var quote byte
	escaped, uncertain := false, false
	appendPart := func(part string) {
		words := strings.Fields(part)
		// Only literal words can be matched safely without interpreting a shell.
		if len(words) == 0 {
			uncertain = true
			return
		}
		if strings.ContainsAny(part, "'\"\\$`<>()") {
			uncertain = true
			stripped := strings.NewReplacer("'", "", "\"", "", "\\", "").Replace(part)
			parts = append(parts, strings.Join(strings.Fields(stripped), " "))
			return
		}
		word := words[0]
		if strings.ContainsAny(word, "=*?[]{}~") {
			uncertain = true
			return
		}
		switch filepath.Base(word) {
		case "command", "env", "sudo", "xargs", "eval", "exec", "sh", "bash", "zsh", "pwsh", "cmd":
			uncertain = true
			return
		}
		parts = append(parts, strings.Join(words, " "))
	}
	for i := 0; i < len(command); i++ {
		c := command[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && quote != '\'' {
			uncertain = true
			escaped = true
			continue
		}
		if quote != '\'' && (c == '`' || c == '$' && i+1 < len(command) && command[i+1] == '(') {
			uncertain = true
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if strings.ContainsRune("<>()", rune(c)) {
			uncertain = true
		}
		if c == '&' && (i+1 >= len(command) || command[i+1] != '&') {
			uncertain = true
		}
		if c != '&' && c != '|' && c != ';' && c != '\n' {
			continue
		}
		appendPart(command[start:i])
		if (c == '&' || c == '|') && i+1 < len(command) && command[i+1] == c {
			i++
		}
		start = i + 1
	}
	appendPart(command[start:])
	return parts, uncertain || escaped || quote != 0
}

func outside(path string) bool {
	return filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, "../") || strings.HasPrefix(path, "~/")
}

// Canonical command forms are used only for denies, never to grant access to
// an executable at an arbitrary path.
func denyCommand(command string) string {
	words := strings.Fields(basenameCommand(command))
	if len(words) == 0 {
		return command
	}
	if words[0] == "git" {
		i := 1
		for i < len(words) && strings.HasPrefix(words[i], "-") {
			option := words[i]
			i++
			if option == "-C" || option == "-c" || option == "--git-dir" || option == "--work-tree" || option == "--namespace" || option == "--config-env" {
				i++
			}
		}
		if i <= len(words) {
			return strings.Join(append([]string{"git"}, words[i:]...), " ")
		}
	}
	if words[0] == "rm" {
		recursive, force := false, false
		for _, word := range words[1:] {
			if word == "--" {
				break
			}
			if word == "--recursive" {
				recursive = true
			}
			if word == "--force" {
				force = true
			}
			if strings.HasPrefix(word, "-") && !strings.HasPrefix(word, "--") {
				recursive = recursive || strings.ContainsAny(word, "rR")
				force = force || strings.Contains(word, "f")
			}
		}
		if recursive && force {
			return "rm -rf " + strings.Join(words[1:], " ")
		}
	}
	return strings.Join(words, " ")
}

func readOnlyCommand(command string) bool {
	words := strings.Fields(command)
	if len(words) == 0 {
		return false
	}
	if words[0] == "git" {
		return len(words) > 1 && slices.Contains([]string{"status", "diff", "log", "show"}, words[1]) && !strings.Contains(command, "--output") && !strings.Contains(command, "--ext-diff") && !strings.Contains(command, "--textconv")
	}
	if !slices.Contains([]string{"cat", "head", "tail", "ls", "pwd", "wc", "rg", "grep", "find"}, words[0]) {
		return false
	}
	for _, word := range words[1:] {
		if slices.Contains([]string{"-exec", "-execdir", "-ok", "-okdir", "-delete", "-fprint", "-fprintf", "-fls"}, word) || strings.HasPrefix(word, "--pre") {
			return false
		}
	}
	return true
}
func readOnlyRule(pattern string) bool {
	for _, p := range expandBraces(pattern) {
		fields := strings.Fields(p)
		if len(fields) == 0 {
			return false
		}
		if fields[0] == "git" {
			if len(fields) < 2 || !slices.Contains([]string{"status", "diff", "log", "show"}, strings.TrimSuffix(fields[1], "*")) {
				return false
			}
		} else if !slices.Contains([]string{"cat", "head", "tail", "ls", "pwd", "wc", "rg", "grep", "find"}, strings.TrimSuffix(fields[0], "*")) {
			return false
		}
	}
	return true
}

func allowedReadCommand(p config.Policy, role, command string) bool {
	for _, rule := range p.Rules {
		if rule.Decision == Allow && readOnlyRule(rule.Match) && readOnlyCommand(command) && matches(rule, Action{Kind: "exec", Target: command, Role: role}) {
			return true
		}
	}
	return false
}
func matchesExplicitOutside(rule config.Rule, action Action) bool {
	for _, pattern := range expandBraces(rule.Match) {
		if !outside(pattern) {
			continue
		}
		rule.Match = pattern
		if matches(rule, action) {
			return true
		}
	}
	return false
}
