package policy

import (
	"slices"
	"strconv"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
)

// RuleAgentLaunch denies a worker that starts claude or codex itself: such a
// run is outside duolog's routing and quota, and a looser native mode would
// give it the worker's reach.
const RuleAgentLaunch = "agent launch"

// agentPackages are the npm packages of the two CLIs.
var agentPackages = []string{"@anthropic-ai/claude-code", "@openai/codex"}

// commandPrefixes run the command that follows them, so the program after
// them is still in command position.
var commandPrefixes = []string{
	"env", "time", "nohup", "exec", "command", "builtin", "eval", "nice", "ionice", "sudo", "doas", "timeout", "stdbuf", "setsid", "xargs", "caffeinate",
	"sh", "bash", "zsh", "dash", "ksh", "fish", "cmd", "start", "call", "powershell", "pwsh", "&",
}

// startsAgent reports whether a command starts claude or codex anywhere: by
// any program path or Windows launcher (ProgramName), behind a prefix such as
// env, VAR=x, time, nohup or cmd /c, inside sh -c '...', or through a package
// runner (npx, pnpm dlx, npm exec, bunx, yarn dlx) or node running the CLI's
// script. Only a word in command position counts. In a POSIX line that
// shellData can read, quoted text and here-document bodies that only data
// programs receive are left out first, so a report that names either agent
// starts nothing. Any other line is read word by word, quotes or not.
func startsAgent(command, shell string) bool {
	if shell != "powershell" {
		if masked, ok := shellData(command); ok {
			command = masked
		}
	}
	for _, words := range protectedWords(command, shell) {
		for i, word := range words {
			if !commandPosition(words[:i]) {
				break
			}
			if prefixValue(words, i) {
				continue // an option's value, such as the user of sudo -u
			}
			name := ProgramName(word)
			if name == "claude" || name == "codex" || agentRunner(name, words[i+1:]) {
				return true
			}
		}
	}
	return false
}

// agentRule reports whether an exec rule names only claude or codex, such as
// the default {claude,codex} and {claude,codex} *. Such a rule judges the
// commands as commandParts reads them, as startsAgent does.
func agentRule(rule config.Rule) bool {
	if rule.Action != "exec" {
		return false
	}
	for _, alternative := range expandBraces(rule.Match) {
		word, _, _ := strings.Cut(strings.TrimSpace(alternative), " ")
		if name := ProgramName(word); name != "claude" && name != "codex" {
			return false
		}
	}
	return true
}

// valueOptions are, per prefix, the options whose value is the next word,
// such as exec -a NAME, timeout -s SIGNAL and sudo -u USER.
var valueOptions = map[string][]string{
	"exec":    {"-a"},
	"timeout": {"-s", "-k", "--signal", "--kill-after"},
	"sudo":    {"-u", "-g", "--user", "--group"},
	"doas":    {"-u"},
	"env":     {"-u", "--unset"},
	"xargs":   {"-a", "-I", "-d", "-E"},
}

// prefixValue reports whether words[i] is the value of an option of the
// nearest prefix before it.
func prefixValue(words []string, i int) bool {
	if i == 0 {
		return false
	}
	option := strings.Trim(words[i-1], `"'`)
	for j := i - 1; j >= 0; j-- {
		if options, ok := valueOptions[ProgramName(strings.Trim(words[j], `"'`))]; ok {
			return slices.Contains(options, option)
		}
	}
	return false
}

// commandPosition reports whether the words before a word only lead up to a
// command: prefixes, their options and option values, numbers and VAR=value
// assignments.
func commandPosition(before []string) bool {
	for i, word := range before {
		w := strings.Trim(word, `"'`)
		switch {
		case prefixValue(before, i):
		case slices.Contains(commandPrefixes, ProgramName(w)):
		case strings.HasPrefix(w, "-") || strings.HasPrefix(w, "/") && len(w) <= 3:
		case strings.Contains(w, "=") && !strings.HasPrefix(w, "="):
		default:
			if _, err := strconv.ParseFloat(strings.TrimRight(w, "smhd"), 64); err != nil {
				return false
			}
		}
	}
	return true
}

// agentRunner reports whether program name with its arguments runs one of the
// CLIs: a package runner naming its package, or a script host running a file
// from it.
func agentRunner(name string, args []string) bool {
	switch name {
	case "npx", "pnpx", "bunx":
	case "npm":
		if len(args) == 0 || args[0] != "exec" && args[0] != "x" {
			return false
		}
	case "pnpm", "yarn":
		if len(args) == 0 || args[0] != "dlx" && args[0] != "exec" {
			return false
		}
	case "bun":
		if len(args) == 0 || args[0] != "x" {
			return slices.ContainsFunc(args, agentScript)
		}
	case "node", "deno", "tsx":
		return slices.ContainsFunc(args, agentScript)
	default:
		return false
	}
	return slices.ContainsFunc(args, agentPackage)
}

// agentPackage reports whether a package runner argument names one of the
// CLI packages: the name with or without a version or an npm: prefix, a URL
// or path inside the package, or its tarball.
func agentPackage(word string) bool {
	a := strings.TrimPrefix(strings.ToLower(strings.Trim(word, `"'`)), "npm:")
	for _, pkg := range agentPackages {
		if a == pkg || strings.HasPrefix(a, pkg+"@") {
			return true
		}
	}
	return agentScript(word) || agentTarball(a)
}

// agentScript reports whether a word is a file inside one of the CLI
// packages, or an npm: specifier for one (deno run npm:<pkg>).
func agentScript(word string) bool {
	w := strings.ToLower(pathutil.Slashes(strings.Trim(word, `"'`)))
	for _, pkg := range agentPackages {
		if strings.Contains(w, pkg+"/") || w == "npm:"+pkg || strings.HasPrefix(w, "npm:"+pkg+"@") {
			return true
		}
	}
	return false
}

// agentTarball reports whether a word is a tarball of one of the CLIs, as
// the registry names it (claude-code-1.0.0.tgz) or npm pack does
// (anthropic-ai-claude-code-1.0.0.tgz).
func agentTarball(word string) bool {
	w := strings.ToLower(pathutil.Slashes(word))
	if !strings.HasSuffix(w, ".tgz") && !strings.HasSuffix(w, ".tar.gz") {
		return false
	}
	base := w[strings.LastIndex(w, "/")+1:]
	for _, prefix := range []string{"claude-code-", "anthropic-ai-claude-code-", "codex-", "openai-codex-"} {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}
	return false
}
