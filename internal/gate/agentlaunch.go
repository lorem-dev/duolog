package gate

import (
	"slices"
	"strconv"
	"strings"

	"github.com/lorem-dev/duolog/internal/pathutil"
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
	"env", "time", "nohup", "exec", "command", "builtin", "nice", "ionice", "sudo", "doas", "timeout", "stdbuf", "setsid", "xargs", "caffeinate",
	"sh", "bash", "zsh", "dash", "ksh", "fish", "cmd", "start", "call", "powershell", "pwsh", "&",
}

// startsAgent reports whether a command starts claude or codex anywhere: by
// any program path or Windows launcher (ProgramName), behind a prefix such as
// env, VAR=x, time, nohup or cmd /c, inside sh -c '...', or through a package
// runner (npx, pnpm dlx, npm exec, bunx, yarn dlx) or node running the CLI's
// script.
func startsAgent(command, shell string) bool {
	for _, words := range protectedWords(command, shell) {
		for i, word := range words {
			if !commandPosition(words[:i]) {
				break
			}
			name := ProgramName(word)
			if name == "claude" || name == "codex" || agentRunner(name, words[i+1:]) {
				return true
			}
		}
	}
	return false
}

// commandPosition reports whether the words before a word only lead up to a
// command: prefixes, their options, numbers and VAR=value assignments.
func commandPosition(before []string) bool {
	for _, word := range before {
		w := strings.Trim(word, `"'`)
		switch {
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
	for _, arg := range args {
		a := strings.ToLower(strings.Trim(arg, `"'`))
		for _, pkg := range agentPackages {
			if a == pkg || strings.HasPrefix(a, pkg+"@") {
				return true
			}
		}
	}
	return false
}

// agentScript reports whether a word is a file inside one of the CLI packages.
func agentScript(word string) bool {
	w := strings.ToLower(pathutil.Slashes(strings.Trim(word, `"'`)))
	for _, pkg := range agentPackages {
		if strings.Contains(w, pkg+"/") {
			return true
		}
	}
	return false
}
