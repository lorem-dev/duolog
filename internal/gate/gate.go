// Package gate decides worker permissions without a model call.
package gate

import (
	"fmt"
	"github.com/lorem-dev/duolog/internal/pathutil"
	"path/filepath"
	"regexp"
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

const RuleOutside = "outside work root"

type Action struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Role   string `json:"role"`
	Shell  string `json:"shell,omitempty"`
	// Work and Cwd resolve the paths inside a read command. They are not logged.
	Work string `json:"-"`
	Cwd  string `json:"-"`
	// State is the project root whose .duolog folder recursive reads must avoid.
	State  string `json:"-"`
	Search bool   `json:"-"`
	Glob   string `json:"-"`
	// MCPExact means the hook named the MCP server as configured, so Target
	// holds its exact name rather than the folded one.
	MCPExact bool `json:"-"`
	// Peer is the entry ID of the peer pane that asks (DUOLOG_PEER). Only that
	// peer may write its report file.
	Peer string `json:"-"`
	// ProtectedRoots are the telemetry and balance roots. No worker path may
	// reach them, whatever the policy or mode.
	ProtectedRoots []string `json:"-"`
	// OwnResult means a read targets the saved tool results of the worker's
	// own Claude session; see OwnToolResults. The hook sets it from the
	// session id, never the worker.
	OwnResult bool `json:"-"`
	// Temp is the hook's ClaudeTemp folders, whose files read commands may read.
	Temp []string `json:"-"`
}

type Decision struct {
	Kind   string    `json:"decision"`
	Reason string    `json:"reason"`
	Rule   string    `json:"rule"`
	Mode   mode.Mode `json:"mode"`
	// Native is the worker's native permission mode from DUOLOG_NATIVE.
	Native string `json:"native,omitempty"`
}

// PersonOnly identifies an outside-work-root ask that needs the person's answer.
func PersonOnly(d Decision) bool { return d.Kind == Ask && d.Rule == RuleOutside }

// Evaluate applies denies before role and mode limits. Lead restrictions always
// apply: no project setting can grant access that the lead does not have.
func Evaluate(p config.Policy, lead LeadRules, m mode.Mode, a Action) Decision {
	if lead.Mode != "" {
		m = mode.Lower(m, lead.Mode)
	}
	decision := func(kind, reason, rule string) Decision {
		if kind == Ask && a.Kind == "exec" && ExecOutside(a) {
			rule = RuleOutside
		}
		return Decision{Kind: kind, Reason: reason, Rule: rule, Mode: m}
	}
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
		if pathutil.Windows(a.Target) {
			a.Shell = "windows"
		}
		if usesWindowsNames(a) {
			target, ok := windowsTarget(a.Target)
			if !ok {
				return decision(Deny, "a Windows stream name can reach a denied file", "windows name")
			}
			a.Target = target
		}
		a.Target = cleanPath(a.Target)
	}
	if a.Kind == "spawn" {
		return decision(Deny, "workers cannot delegate", "worker depth")
	}
	if d, ok := protectedDecision(a, m); ok {
		return d
	}
	// Before parsing, which gives up on wrappers such as env and asks instead.
	if a.Kind == "exec" && startsAgent(a.Target, a.Shell) {
		return decision(Deny, "workers cannot start claude or codex; such a run is outside duolog's routing and quota", RuleAgentLaunch)
	}
	if a.Kind == "read" && stateReadAction(a) {
		return decision(Deny, "duolog state is not readable by workers", RuleState)
	}
	parts := []string{a.Target}
	uncertain := false
	if a.Kind == "exec" {
		parts, uncertain = readShellParts(a.Target, a.Shell)
		uncertain = uncertain || needsReview(parts)
		if recursiveStateRead(a, parts, uncertain) {
			return decision(Deny, "duolog state is not readable by workers", RuleState)
		}
	}
	// A peer's own report file is the one state path a worker may write. Lead
	// denies still apply; the project's state deny and the role and mode limits
	// do not, so a read-only reviewer can hand back its report.
	report := a.Kind == "write" && ReportTarget(a.Peer, a.Target)
	lineWrites := a.Kind == "exec" && lineCanWrite(a.Target, parts)
	// Check every part for a deny before any ask or allow can return.
	for _, part := range parts {
		partAction := a
		partAction.Target = part
		baseAction := partAction
		if a.Kind == "exec" {
			baseAction.Target = denyCommand(part)
			if a.Shell == "powershell" {
				baseAction.Target = denyPowerShell(part)
			}
		}
		// Preserve path-specific denies as well as basename command denies.
		for _, rule := range lead.Deny {
			if matches(rule, partAction) || matches(rule, baseAction) {
				return decision(Deny, "denied by lead settings", "lead: "+rule.Action+" "+rule.Match)
			}
		}
		for _, rule := range p.Rules {
			if rule.Decision == Deny && !report && (matches(rule, partAction) || matches(rule, baseAction)) {
				return decision(Deny, "denied by project policy", rule.Action+" "+rule.Match)
			}
		}
		if a.Kind == "exec" {
			if d, ok := redirectDeny(p, lead, m, a, part); ok {
				return d
			}
			if d, ok := inputDeny(p, lead, m, a, part); ok {
				return d
			}
		}
		if a.Kind == "exec" && !readOnlyCommand(part) {
			if d, ok := hiddenDeny(p, lead, m, a, part, lineWrites); ok {
				return d
			}
		}
	}
	// The parser splits at a lone &, so >& file reaches the redirect checks
	// only in the raw parts, which keep it whole. They also keep a quoted
	// script whole.
	if a.Kind == "exec" {
		for _, part := range rawExecParts(a.Target) {
			if d, ok := rawPartDeny(p, lead, m, a, part); ok {
				return d
			}
		}
	}

	// Reading by command honours the rules of reading by tool. A deny stands even
	// when the shell syntax is uncertain. Asks wait for role and mode checks.
	var readAsk *Decision
	if a.Kind == "read" && a.Search && readGlobMatchesDeny(p, lead, a, []string{a.Glob}) {
		d := decision(Ask, "a search glob can read a file that policy denies; name the files", "read command")
		readAsk = &d
	}
	if a.Kind == "exec" {
		if d, ok := readCommandDecision(p, lead, m, a, parts, uncertain); ok {
			if d.Kind == Deny {
				return d
			}
			readAsk = &d
		}
	}

	if report {
		return decision(Allow, "the peer's own report file", RuleReport)
	}
	if a.Kind == "internal" || a.Kind == "answer" {
		return decision(Allow, "returns the worker answer", "answer tool")
	}

	// The lead talks to the user; a worker that waits for an answer only stalls.
	if a.Kind == "unknown" && (strings.HasPrefix(a.Target, "request_user_input") || a.Target == "AskUserQuestion") {
		return decision(Deny, "a worker cannot ask the user; return done false with the question in needs", "ask user")
	}
	if d, ok := unjudged(p, a, heldReadOnly(m, lead), decision); ok {
		return d
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
				if rule.Decision == Allow && readOnlyRule(rule.Match) && readOnlyCommand(part) && matches(rule, Action{Kind: "exec", Target: part, Role: a.Role, Shell: a.Shell}) {
					allowed = true
					break
				}
			}
			if !allowed {
				return decision(Deny, "read-only role requires a named read-only command", "role sandbox")
			}
		}
	}
	// Plan's limit is hard, so it comes before every return that asks (planLimit).
	if d, ok := planLimit(p, m, a, parts, uncertain, lineWrites, readAsk, decision); ok {
		return d
	}
	if a.Kind == "read" && a.OwnResult && outside(a.Target) {
		return decision(Allow, "the worker's own saved tool result or session temp file", "own tool results")
	}
	if (a.Kind == "read" || a.Kind == "write") && outside(a.Target) {
		for _, rule := range p.Rules {
			if rule.Decision != Deny && matchesExplicitOutside(rule, a) {
				if a.Kind == "write" && m == mode.Ask {
					return decision(Ask, "ask mode requires approval for writes", "mode ask")
				}
				return decision(rule.Decision, "explicit outside path rule", rule.Action+" "+rule.Match)
			}
		}
		return decision(Ask, "path outside work root requires approval", RuleOutside)
	}
	// apply_patch and other writes without a verified target cannot be allowed,
	// including in full mode. A known read ask still needs the mode check below.
	if readAsk == nil && (a.Target == "" || uncertain) {
		return decision(Ask, "tool target or shell syntax requires approval", "unverified action")
	}
	if readAsk != nil {
		return *readAsk
	}
	if a.Kind == "write" && m == mode.Ask {
		return decision(Ask, "ask mode requires approval for writes", "mode ask")
	}
	result := decision(Allow, "allowed by policy", "policy")
	wildOutside := wildcardAllowsOutside(a)
	for _, part := range parts {
		d := partDecision(p, m, a, part, decision, wildOutside)
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

// mcpDecision allows an MCP tool only by a rule that names it. duolog cannot
// tell whether a tool reads or writes, so no mode or role allows one by
// itself, and full mode does not turn the ask into an allow. held, from
// heldReadOnly, admits only a tool that a rule marks read_only or one of the
// default read-only tools, and denies the rest. Denies were checked before.
func mcpDecision(p config.Policy, a Action, held string, decision func(kind, reason, rule string) Decision) Decision {
	for _, rule := range p.Rules {
		if rule.Decision == Deny || !mcpRuleAllows(rule, a) {
			continue
		}
		if held != "" && !rule.ReadOnly && !defaultReadOnlyMCP(a) {
			continue
		}
		if rule.Decision == Allow {
			return decision(Allow, "matched project policy", rule.Action+" "+rule.Match)
		}
		break
	}
	if held != "" {
		return decision(Deny, "only reads are permitted here, and no rule marks this MCP tool read_only", held)
	}
	if p.OnUnmatched == Deny {
		return decision(Deny, "no matching policy rule", "on_unmatched")
	}
	return decision(Ask, "MCP tool requires approval", "mcp tool")
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
	return pathutil.Clean(path)
}

func matches(r config.Rule, a Action) bool {
	if legacyMCP(r, a) {
		return true
	}
	if r.Action != a.Kind || len(r.Roles) > 0 && !slices.Contains(r.Roles, a.Role) {
		return false
	}
	for _, pattern := range expandBraces(r.Match) {
		if a.Kind == "read" || a.Kind == "write" {
			pattern = strings.TrimPrefix(pathutil.Clean(pattern), "./")
			if runtime.GOOS == "darwin" || runtime.GOOS == "windows" || a.Shell == "windows" || pathutil.Windows(r.Match) || pathutil.Windows(a.Target) {
				pattern = strings.ToLower(pattern)
				a.Target = strings.ToLower(a.Target)
			}
			ok, err := doublestar.Match(pattern, a.Target)
			if err == nil && ok {
				return true
			}
		} else if a.Kind == "mcp" {
			if starMatch(mcpFlat(pattern), mcpFlat(a.Target)) {
				return true
			}
		} else if starMatch(execCase(pattern, a.Shell), execCase(a.Target, a.Shell)) {
			return true
		}
	}
	return false
}

func validateRule(r config.Rule) error {
	if r.Match == "" {
		return fmt.Errorf("empty %s permission pattern", r.Action)
	}
	if r.ReadOnly && (r.Action != "mcp" || r.Decision != Allow) {
		return fmt.Errorf("read_only marks an mcp allow rule only, not %s %s %q", r.Decision, r.Action, r.Match)
	}
	switch r.Action {
	case "read", "write":
		for _, pattern := range expandBraces(r.Match) {
			if !doublestar.ValidatePattern(pathutil.Slashes(pattern)) {
				return fmt.Errorf("invalid %s permission pattern %q", r.Action, r.Match)
			}
		}
	case "exec", "net", "spawn", "unknown", "internal", "answer":
	case "mcp":
		for _, pattern := range expandBraces(r.Match) {
			if server, tool, ok := strings.Cut(pattern, "/"); !ok || server == "" || tool == "" {
				return fmt.Errorf("mcp permission pattern %q: write server/tool", r.Match)
			}
		}
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
	return ProgramName(word) + command[len(word):]
}

// quotedLiteralUnsafe lists what a quoted run may not hold. Whitespace and
// control characters would split one shell word into several once the quotes
// are gone, and glob, brace and tilde characters would read as patterns the
// shell never expands. A backslash is refused because removing the quotes
// removes it too. Double quotes also expand $ and backticks; single quotes keep
// them literal.
// A read command may still quote a word with spaces: quotedWords keeps it one word.
const (
	quotedLiteralUnsafe       = " \t\n\r\v\f*?[]{}~'\"\\"
	doubleQuotedLiteralUnsafe = quotedLiteralUnsafe + "$`"
)

// quotedSpace stands for a space inside quotes, so that strings.Fields keeps
// the quoted word whole; readCommandTargets turns it back into a space. A
// command that holds it itself is uncertain.
const quotedSpace = "\x00"

// devNullRedirects only throw output away. A read command may carry them; any
// other redirect can write a file.
var devNullRedirects = []string{"2>/dev/null", ">/dev/null", "2>&1"}

// devNullRedirect returns the length of a devNullRedirects word at command[i],
// or 0.
func devNullRedirect(command string, i int) int {
	if i == 0 || command[i-1] != ' ' && command[i-1] != '\t' {
		return 0
	}
	for _, redirect := range devNullRedirects {
		end := i + len(redirect)
		if strings.HasPrefix(command[i:], redirect) && (end == len(command) || strings.IndexByte(" \t;|&\n", command[end]) >= 0) {
			return len(redirect)
		}
	}
	return 0
}

// quotedWords splits a part whose quotes only group literal text into the
// words the shell passes on, with quotedSpace for each space inside quotes.
func quotedWords(part string) []string {
	var b strings.Builder
	var quote byte
	for i := 0; i < len(part); i++ {
		c := part[i]
		switch {
		case quote == 0 && (c == '\'' || c == '"'):
			quote = c
		case c == quote:
			quote = 0
		case quote != 0 && c == ' ':
			b.WriteString(quotedSpace)
		default:
			b.WriteByte(c)
		}
	}
	return strings.Fields(b.String())
}

// literalQuotes reports whether the quotes in part only group literal text,
// so that removing them leaves the same words the shell passes on: they are
// balanced, each quoted run is non-empty and holds none of quotedLiteralUnsafe,
// and nothing outside them redirects, substitutes or escapes. With spaces, a
// quoted run may hold spaces, which quotedWords keeps inside one word.
func literalQuotes(part string, spaces bool) bool {
	for i := 0; i < len(part); i++ {
		c := part[i]
		if c != '\'' && c != '"' {
			if strings.IndexByte("\\$`<>()", c) >= 0 {
				return false
			}
			continue
		}
		unsafe := quotedLiteralUnsafe
		if c == '"' {
			unsafe = doubleQuotedLiteralUnsafe
		}
		if spaces {
			unsafe = strings.Replace(unsafe, " ", "", 1)
		}
		end := strings.IndexByte(part[i+1:], c)
		if end <= 0 || strings.ContainsAny(part[i+1:i+1+end], unsafe) {
			return false
		}
		i += end + 1
	}
	return true
}

// ShellParts splits a command using the gate's conservative shell parser. A
// quoted word with spaces and a /dev/null redirect stay uncertain here; the
// lead guard looks inside uncertain commands for duolog allow.
func ShellParts(command, shell string) ([]string, bool) {
	if shell == "powershell" {
		return powershellParts(command)
	}
	return shellParts(command, false)
}

// readShellParts is ShellParts for Evaluate: a read command may also quote a
// word with spaces and throw its output into /dev/null.
func readShellParts(command, shell string) ([]string, bool) {
	if shell == "powershell" {
		return powershellParts(command)
	}
	return shellParts(command, true)
}

// commandAfterKeywords drops the shell keywords in front of a command, since
// the shell runs the command after them as it is: !, do, then, else, elif, if,
// while, until, {, time and time -p, an opening (, a case header with its
// first pattern, and a later case pattern such as b). Every rule then judges
// the command itself. A part that is only keywords, such as done or a for
// header, stays whole.
func commandAfterKeywords(part string) string {
	for {
		word, after := nextShellWord(part)
		subshell := strings.HasPrefix(word, "(") && !strings.HasPrefix(word, "((")
		if subshell {
			// (ls) and ( ls ) run ls.
			inner := strings.TrimSuffix(strings.TrimPrefix(word, "("), ")")
			switch {
			case inner != "":
				part = inner + after
			case strings.TrimSpace(after) != "":
				part = after
			default:
				return part
			}
			continue
		}
		next, _ := nextShellWord(after)
		if next == "" {
			return part
		}
		switch {
		case slices.Contains([]string{"!", "do", "then", "else", "elif", "if", "while", "until", "{"}, word):
			part = after
		case word == "time":
			// The keyword takes only -p; any other option is the time program's,
			// which stays a wrapper for review.
			option, rest := nextShellWord(after)
			if option == "-p" {
				option, _ = nextShellWord(rest)
				after = rest
			}
			if option == "" || strings.HasPrefix(option, "-") {
				return part
			}
			part = after
		case word == "case":
			_, rest := nextShellWord(after)
			in, rest := nextShellWord(rest)
			if in != "in" {
				return part
			}
			// The first pattern runs up to the word that ends in ), as in a),
			// a ) and a | b ). A part where none does stays whole, and
			// canWrite takes it to write.
			pattern := ""
			for !strings.HasSuffix(pattern, ")") {
				if pattern, rest = nextShellWord(rest); pattern == "" {
					return part
				}
			}
			if command, _ := nextShellWord(rest); command == "" {
				return part
			}
			part = rest
		case strings.HasPrefix(word, "(("):
			return part
		case strings.HasSuffix(word, ")"):
			// A later case pattern; shellParts also judges the whole part,
			// since the word may instead end a subshell.
			part = after
		case next == ")":
			// A later case pattern with a space, as in b ). The parser has
			// already split b | c ) at the |.
			rest := after
			for {
				pattern, more := nextShellWord(rest)
				if pattern == "" {
					return part
				}
				rest = more
				if strings.HasSuffix(pattern, ")") {
					break
				}
			}
			if command, _ := nextShellWord(rest); command == "" {
				return part
			}
			part = rest
		default:
			return part
		}
	}
}

// nextShellWord returns the first word of s and the text after it.
func nextShellWord(s string) (string, string) {
	s = strings.TrimLeft(s, " \t\r\n")
	end := strings.IndexAny(s, " \t\r\n")
	if end < 0 {
		return s, ""
	}
	return s[:end], s[end:]
}

// shellParts is deliberately conservative, not a shell interpreter. Quoted
// separators remain arguments; substitutions and unsupported syntax need review.
// With reads, a read command's quoted spaces and /dev/null redirects are read.
func shellParts(command string, reads bool) ([]string, bool) {
	var parts []string
	start, redirect := 0, -1
	var quote byte
	escaped, uncertain := false, false
	var appendOne func(part string, redirected bool)
	appendPart := func(part string, redirected bool) {
		command := commandAfterKeywords(part)
		// A word that ends in ) may be a case pattern or the end of a
		// subshell, so a part with ) is judged both ways. Any ) makes the line
		// uncertain, so the extra part can only add a deny or an ask.
		if command != part && strings.Contains(part, ")") {
			appendOne(part, redirected)
		}
		appendOne(command, redirected)
	}
	appendOne = func(part string, redirected bool) {
		if strings.HasPrefix(strings.TrimSpace(part), "rg ") {
			part = unquoteRgGlobs(part)
		}
		words := strings.Fields(part)
		quoted := false
		// Only literal words can be matched safely without interpreting a shell.
		if len(words) == 0 {
			uncertain = true
			return
		}
		if strings.ContainsAny(part, "'\"\\$`<>()") {
			stripped := strings.NewReplacer("'", "", "\"", "", "\\", "").Replace(part)
			words = strings.Fields(stripped)
			switch {
			case literalQuotes(part, false) && len(words) > 0:
			case reads && literalQuotes(part, true) && readOnlyCommand(strings.Join(quotedWords(part), " ")):
				words = quotedWords(part)
			default:
				uncertain = true
				parts = append(parts, strings.Join(words, " "))
				return
			}
			quoted = true
		}
		// A redirect into /dev/null writes nothing only on a read command.
		if redirected && !readOnlyCommand(strings.Join(words, " ")) {
			uncertain = true
			parts = append(parts, strings.Join(words, " "))
			return
		}
		word := words[0]
		// A quoted part still reaches the deny checks when it needs review.
		review := func() {
			uncertain = true
			if quoted {
				parts = append(parts, strings.Join(words, " "))
			}
		}
		// A program path with .. could start anything under a rule such as
		// node_modules/.bin/tsc*.
		if strings.ContainsAny(word, "=*?[]{}~") || slices.Contains(strings.Split(pathutil.Slashes(word), "/"), "..") {
			review()
			return
		}
		switch filepath.Base(word) {
		case "command", "builtin", "env", "sudo", "xargs", "eval", "exec", "sh", "bash", "zsh", "pwsh", "powershell", "cmd",
			"time", "nice", "ionice", "timeout", "nohup", "stdbuf", "setsid", "busybox", "doas", "watch", "parallel":
			review()
			// The wrapper runs the command after it, which every deny must
			// still see. The line is uncertain, so this part can only add a
			// deny or an ask.
			if start := execOperandStart(words) - 1; start > 0 && start < len(words) {
				appendPart(strings.Join(words[start:], " "), redirected)
			}
			return
		}
		parts = append(parts, strings.Join(words, " "))
	}
	// Redirects into /dev/null are blanked out of the words and noted per part.
	blanked := []byte(command)
	redirected := false
	for i := 0; i < len(command); i++ {
		c := command[i]
		if c == quotedSpace[0] {
			uncertain = true
		}
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
		if n := devNullRedirect(command, i); reads && n > 0 {
			for j := i; j < i+n; j++ {
				blanked[j] = ' '
			}
			redirected = true
			i += n - 1
			continue
		}
		// Brace expansion turns one word into several, so a word the gate reads can become a denied path.
		if strings.ContainsRune("<>(){", rune(c)) {
			uncertain = true
		}
		if c == '>' {
			redirect = i
		}
		if c == '&' && (i+1 >= len(command) || command[i+1] != '&') {
			uncertain = true
		}
		// >| is a clobber redirect, so the word after it is a file, not a program.
		if c != '&' && c != '|' && c != ';' && c != '\n' || c == '|' && redirect == i-1 {
			continue
		}
		appendPart(string(blanked[start:i]), redirected)
		redirected = false
		if (c == '&' || c == '|') && i+1 < len(command) && command[i+1] == c {
			i++
		}
		start = i + 1
	}
	appendPart(string(blanked[start:]), redirected)
	return parts, uncertain || escaped || quote != 0
}

func outside(path string) bool {
	return pathutil.Outside(path)
}

// Canonical command forms are used only for denies, never to grant access to
// an executable at an arbitrary path.
func denyCommand(command string) string {
	words := strings.Fields(basenameCommand(command))
	if len(words) > 0 && strings.EqualFold(words[0], "Remove-Item") {
		for _, word := range words[1:] {
			if strings.EqualFold(word, "-Recurse") || strings.HasPrefix(strings.ToLower(word), "-recurse:") {
				return "remove-item -recurse " + strings.Join(words[1:], " ")
			}
		}
	}
	if len(words) == 0 {
		return command
	}
	if strings.EqualFold(words[0], "git") {
		i := 1
		for i < len(words) && strings.HasPrefix(words[i], "-") {
			option := words[i]
			i++
			if option == "-C" || option == "-c" || option == "--git-dir" || option == "--work-tree" || option == "--namespace" || option == "--config-env" {
				i++
			}
		}
		if i < len(words) && strings.EqualFold(words[i], "reset") && slices.Contains(words[i:], "--hard") {
			return strings.ToLower("git reset --hard " + strings.Join(words[i+1:], " "))
		}
		if i <= len(words) {
			return strings.ToLower(strings.Join(append([]string{"git"}, words[i:]...), " "))
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

// readCommands only print. find and rg can also write or run, so readOnlyCommand
// checks their options.
// diff is not here: diff -r and two folders print files that policy denies.
var readCommands = []string{"cat", "head", "tail", "ls", "pwd", "wc", "grep", "get-content", "get-childitem", "get-location", "test-path", "select-string", "get-item",
	"stat", "du", "cut", "nl", "comm", "realpath", "which", "basename", "dirname", "shasum", "md5"}

// checkedReadCommands are reads only with some options, which readOnlyCommand checks.
var checkedReadCommands = []string{"find", "rg", "sed", "jq", "sort", "uniq", "tree", "file"}

func readOnlyCommand(command string) bool {
	raw := strings.Fields(command)
	words := strings.Fields(strings.ToLower(command))
	if len(words) == 0 {
		return false
	}
	if words[0] == "git" {
		return len(words) > 1 && slices.Contains([]string{"status", "diff", "log", "show"}, words[1]) && !strings.Contains(command, "--output") && !strings.Contains(command, "--ext-diff") && !strings.Contains(command, "--textconv") && !strings.Contains(command, "--no-index")
	}
	switch words[0] {
	case "find":
		return findOptionsKnown(words[1:])
	case "rg":
		return rgOptionsKnown(raw[1:])
	case "sed":
		return safeSed(raw)
	case "jq":
		_, ok := jqFiles(raw[1:])
		return ok
	case "grep", "sort", "uniq", "file":
		return checkedOptions[words[0]].known(raw[1:])
	case "tree":
		// -o writes a file, and -R with -H writes an HTML page into each folder.
		return !slices.ContainsFunc(raw[1:], func(w string) bool {
			return strings.HasPrefix(w, "--o") || strings.HasPrefix(w, "--fromfile") || !strings.HasPrefix(w, "--") && strings.HasPrefix(w, "-") && strings.ContainsAny(w, "oR")
		})
	}
	return slices.Contains(readCommands, words[0])
}

var sedPrintRange = regexp.MustCompile(`^([0-9]+|\$)(,([0-9]+|\$))?p$`)

// safeSed accepts only sed -n 'N,Mp' FILE...: it prints a line range and can
// neither write a file nor run a command.
func safeSed(words []string) bool {
	if len(words) < 4 || words[1] != "-n" || !sedPrintRange.MatchString(words[2]) {
		return false
	}
	for _, word := range words[3:] {
		if strings.HasPrefix(word, "-") {
			return false
		}
	}
	return true
}

// readCommandDecision applies read rules to the paths of each read command in
// a shell line. It only answers when a path changes the outcome.
func readCommandDecision(p config.Policy, lead LeadRules, m mode.Mode, a Action, parts []string, uncertain bool) (Decision, bool) {
	shell := a.Shell
	if shell == "powershell" {
		shell = "windows"
	}
	var asked *Decision
	if unquotedRgGlob(a.Target, a.Shell) {
		asked = &Decision{Kind: Ask, Reason: "a shell glob in an rg option can expand onto denied files; quote the glob", Rule: "read command", Mode: m}
	}
	for _, part := range parts {
		if !readOnlyCommand(part) {
			continue
		}
		targets, reason := readCommandTargets(part)
		words := commandWords(part)
		if words[0] == "rg" && readGlobMatchesDeny(p, lead, a, rgGlobs(words[1:])) && asked == nil {
			asked = &Decision{Kind: Ask, Reason: "a search glob can read a file that policy denies; name the files", Rule: "read command", Mode: m}
		}
		if reason != "" && asked == nil {
			asked = reasonAsk(a, m, reason)
		}
		for _, written := range targets {
			// A stream device holds no file, and a redirect such as > /dev/null
			// can read here as a file the command reads: only a deny counts.
			device := streamDevice(expandHome(written))
			target := workTarget(a, written)
			if strings.ContainsAny(target, "*?[{") {
				if statePattern(target) {
					return Decision{Kind: Deny, Reason: "duolog state is not readable by workers", Rule: RuleState, Mode: m}, true
				}
				continue
			}
			d := Evaluate(p, lead, m, Action{Kind: "read", Target: target, Role: a.Role, Shell: shell, OwnResult: ownTempRead(a, written)})
			if d.Kind == Deny {
				d.Reason = "read command: " + d.Reason
				return d, true
			}
			if d.Kind != Allow && !device && (asked == nil || PersonOnly(d)) {
				d.Reason = "read command: " + d.Reason
				asked = &d
			}
		}
	}
	if asked != nil && (!uncertain || PersonOnly(*asked)) {
		return *asked, true
	}
	return Decision{}, false
}
func readOnlyRule(pattern string) bool {
	for _, p := range expandBraces(pattern) {
		fields := strings.Fields(strings.ToLower(p))
		if len(fields) == 0 {
			return false
		}
		if fields[0] == "git" {
			if len(fields) < 2 || !slices.Contains([]string{"status", "diff", "log", "show"}, strings.TrimSuffix(fields[1], "*")) {
				return false
			}
		} else if name := strings.TrimSuffix(fields[0], "*"); !slices.Contains(checkedReadCommands, name) && !slices.Contains(readCommands, name) {
			return false
		}
	}
	return true
}

func allowedReadCommand(p config.Policy, role, command, shell string) bool {
	for _, rule := range p.Rules {
		if rule.Decision == Allow && readOnlyRule(rule.Match) && readOnlyCommand(command) && matches(rule, Action{Kind: "exec", Target: command, Role: role, Shell: shell}) {
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

func execCase(s, shell string) string {
	if shell == "powershell" {
		return strings.ToLower(s)
	}
	return s
}

var namedPowerShell = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-]*(?:\.exe)?$`)

// PowerShell is not POSIX shell. Never interpret expressions, aliases, calls,
// script blocks, pipelines, redirection, expansions, quotes or command chains.
// Still return parts for deny checks before asking about uncertain syntax.
func powershellParts(command string) ([]string, bool) {
	chunks := strings.FieldsFunc(command, func(r rune) bool { return strings.ContainsRune(";|&\r\n", r) })
	uncertain := len(chunks) != 1 || strings.ContainsAny(command, ";|&\r\n'\"$`(){}<>@#=,*?[]")
	var parts []string
	for _, chunk := range chunks {
		words := strings.Fields(chunk)
		if len(words) == 0 {
			uncertain = true
			continue
		}
		switch strings.ToLower(words[0]) {
		case "command", "env", "sudo", "xargs", "eval", "exec", "sh", "bash", "zsh", "pwsh", "powershell", "cmd", "invoke-expression", "iex", "start-process":
			uncertain = true
		}
		if !namedPowerShell.MatchString(words[0]) {
			uncertain = true
		}
		words[0] = strings.TrimSuffix(strings.ToLower(words[0]), ".exe")
		// Cmdlet names, parameter names and Git subcommands are case-insensitive.
		parts = append(parts, strings.ToLower(strings.Join(words, " ")))
	}
	return parts, uncertain || len(parts) == 0
}

// Normalize destructive aliases and abbreviated parameters only for denies.
// They can never confer an allow or a read-only permission.
func denyPowerShell(command string) string {
	words := strings.Fields(strings.ToLower(command))
	if len(words) == 0 {
		return command
	}
	switch words[0] {
	case "remove-item", "ri", "rm", "rmdir", "rd", "del", "erase":
		for _, word := range words[1:] {
			parameter, _, _ := strings.Cut(word, ":")
			if strings.HasPrefix(parameter, "-") && len(parameter) > 1 && strings.HasPrefix("-recurse", parameter) {
				return "remove-item -recurse " + strings.Join(words[1:], " ")
			}
		}
	}
	return denyCommand(command)
}

// mcpFlat writes server/tool as the agents name the tool, mcp__<server>__<tool>,
// folded. Denies compare in this form, so that a name with __ in it, such as
// the server my__wiki, cannot slip past a deny written for the full tool name.
func mcpFlat(s string) string {
	server, tool, _ := strings.Cut(s, "/")
	return "mcp__" + MCPName(server) + "__" + MCPName(tool)
}

// mcpRuleAllows reports whether an allow or ask rule covers a. When the hook
// named the server, the server must match as written: trusted-api and
// trusted_api are two servers. The tool is compared as Claude names it, which
// keeps - apart from _. Codex gives only the folded tool name, so a
// name with more than one place to split matches only a rule without a star.
func mcpRuleAllows(r config.Rule, a Action) bool {
	if legacyMCP(r, a) {
		return true
	}
	if r.Action != a.Kind || len(r.Roles) > 0 && !slices.Contains(r.Roles, a.Role) {
		return false
	}
	flat := mcpFlat(a.Target)
	for _, pattern := range expandBraces(r.Match) {
		switch {
		case a.MCPExact:
			ps, pt, _ := strings.Cut(pattern, "/")
			as, at, _ := strings.Cut(a.Target, "/")
			if starMatch(ps, as) && starMatch(claudePattern.ReplaceAllString(pt, "_"), at) {
				return true
			}
		case strings.Count(flat, "__") > 2:
			if !strings.Contains(pattern, "*") && mcpFlat(pattern) == flat {
				return true
			}
		case starMatch(mcpFlat(pattern), flat):
			return true
		}
	}
	return false
}

// legacyMCP keeps a rule written before MCP tools had their own action, such
// as {action: unknown, match: "mcp__tracker__*"}: it still covers those tools.
func legacyMCP(r config.Rule, a Action) bool {
	if r.Action != "unknown" || a.Kind != "mcp" || len(r.Roles) > 0 && !slices.Contains(r.Roles, a.Role) {
		return false
	}
	for _, pattern := range expandBraces(r.Match) {
		if strings.HasPrefix(pattern, "mcp__") && starMatch(MCPName(pattern), mcpFlat(a.Target)) {
			return true
		}
	}
	return false
}

// claudePattern folds a rule's tool pattern as Claude folds tool names, keeping
// the star.
var claudePattern = regexp.MustCompile(`[^A-Za-z0-9_*-]`)
