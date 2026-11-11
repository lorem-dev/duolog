package session

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

// launchers run a command for an agent without being the agent: shells, and
// programs that wrap one command and stay its parent.
var launchers = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "mksh": true,
	"fish": true, "csh": true, "tcsh": true, "cmd": true, "powershell": true,
	"pwsh": true, "env": true, "nohup": true, "timeout": true, "sandbox-exec": true,
	"codex-linux-sandbox": true,
}

func launcher(name string) bool {
	name = strings.ToLower(filepath.Base(name))
	name = strings.TrimSuffix(strings.TrimPrefix(name, "-"), ".exe")
	return launchers[name]
}

// AgentPID finds the agent process that runs this command's shell, as
// LeadPID does for hooks: the nearest ancestor that is not a shell or a
// launcher. It returns 0 when the system does not name processes or the
// shell has no agent above it.
func AgentPID() int { return agentPID(os.Getppid(), parentProcess) }

func agentPID(pid int, parent func(int) (int, string)) int {
	for steps := 0; pid > 1 && steps < 32; steps++ {
		next, name := parent(pid)
		if name == "" {
			return 0
		}
		if !launcher(name) {
			return pid
		}
		if next <= 1 || next == pid {
			return 0
		}
		pid = next
	}
	return 0
}

// AgentAncestor names the agent, "claude" or "codex", when a Claude Code or
// Codex process is among this process's ancestors, else "". It walks the
// parent chain on Linux, macOS and Windows; elsewhere it finds nothing. An
// agent run through node under another name is not found.
//
// names are the agents' binaries ("claude", "codex-*") and helpers their
// sandbox helper processes, as each agent's descriptor gives them.
func AgentAncestor(names, helpers []string) string {
	return agentAncestorBy(os.Getppid(), parentProcess, func(name string) string { return matchAgent(name, names, helpers) })
}

// ParentAgent names the agent, "claude" or "codex", when this process's
// direct parent is a Claude Code or Codex process, else "". Unlike
// AgentAncestor it looks no further: a shell between the agent and this
// process, as in a model's Bash call, names none. names and helpers are as
// in AgentAncestor. A parent that is node, as an npm install of an agent
// runs, names the agent only when node's first argument is that agent's
// script: a path ending in a key of scripts, itself or, when absolute, where
// its links resolve (the npm bin link). Anything else under node names none.
func ParentAgent(names, helpers []string, scripts map[string]string) string {
	return parentAgentBy(os.Getppid(), parentProcess, processArgs, resolveLinks, func(name string) string { return matchAgent(name, names, helpers) }, scripts)
}

// resolveLinks is path with every link resolved, or "" when it cannot be.
func resolveLinks(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	return resolved
}

// parentAgentBy is the agent that match names for the process ppid, by the
// name that parent gives it, or for node by the script in the arguments
// that args gives it, resolved by resolve when absolute.
func parentAgentBy(ppid int, parent func(int) (int, string), args func(int) []string, resolve func(string) string, match func(string) string, scripts map[string]string) string {
	if ppid <= 1 {
		return ""
	}
	_, name := parent(ppid)
	if !nodeNames[baseName(name)] {
		return match(name)
	}
	script := nodeScript(args(ppid))
	if script == "" {
		return ""
	}
	if agent := scriptAgent(script, scripts); agent != "" {
		return agent
	}
	if !filepath.IsAbs(script) {
		return ""
	}
	return scriptAgent(resolve(script), scripts)
}

// scriptAgent is the agent whose script path ends path, "" for none.
// Backslashes count as slashes, as a Windows path has them.
func scriptAgent(path string, scripts map[string]string) string {
	if path == "" {
		return ""
	}
	slashed := strings.ReplaceAll(path, `\`, "/")
	for end, agent := range scripts {
		if strings.HasSuffix(slashed, "/"+end) {
			return agent
		}
	}
	return ""
}

// nodeNames are the process names of node.
var nodeNames = map[string]bool{"node": true, "nodejs": true}

// nodeScript is the script of a node command line: its first argument, and
// only when it is no option. Any option before the script, of node or of
// another kind, can run other code or take the script's place as its value,
// so a command line with one has no script here.
func nodeScript(args []string) string {
	if len(args) < 2 || args[1] == "" || strings.HasPrefix(args[1], "-") {
		return ""
	}
	return args[1]
}

// matchAgent names the agent that a process name is, by the binaries and
// helpers of the descriptors: a pattern's name is the text before "-*". It
// returns "" for any other program.
func matchAgent(name string, names, helpers []string) string {
	name = baseName(name)
	for _, n := range names {
		stem, _, _ := strings.Cut(n, "-*")
		if ok, _ := path.Match(strings.ToLower(n), name); ok {
			return stem
		}
	}
	for _, h := range helpers {
		if strings.ToLower(h) != name {
			continue
		}
		for _, n := range names {
			if stem, _, _ := strings.Cut(n, "-*"); strings.HasPrefix(name, stem+"-") {
				return stem
			}
		}
	}
	return ""
}

func baseName(name string) string {
	name = strings.ToLower(filepath.Base(strings.ReplaceAll(name, `\`, "/")))
	return strings.TrimSuffix(name, ".exe")
}

func agentAncestor(pid int, parent func(int) (int, string)) string {
	return agentAncestorBy(pid, parent, agentName)
}

func agentAncestorBy(pid int, parent func(int) (int, string), match func(string) string) string {
	for steps := 0; pid > 1 && steps < 64; steps++ {
		next, name := parent(pid)
		if agent := match(name); agent != "" {
			return agent
		}
		if next == pid {
			break
		}
		pid = next
	}
	return ""
}

// agentName names the agent a process name is, "claude" or "codex", by any
// path, with .exe, and as codex-<target triple>; "" for any other program.
func agentName(name string) string {
	name = strings.ToLower(filepath.Base(strings.ReplaceAll(name, `\`, "/")))
	name = strings.TrimSuffix(name, ".exe")
	switch {
	case name == "claude":
		return "claude"
	case name == "codex" || strings.HasPrefix(name, "codex-"):
		return "codex"
	}
	return ""
}

// ValidSessionID accepts the letters, digits, dashes and underscores of
// Claude session IDs and Codex thread IDs, so an ID cannot name a path.
func ValidSessionID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// Newest returns the most recently changed regular file among paths, or "".
// The agents' transcript lookups use it.
func Newest(paths []string) string {
	var best string
	var bestInfo os.FileInfo
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if bestInfo == nil || info.ModTime().After(bestInfo.ModTime()) {
			best, bestInfo = p, info
		}
	}
	return best
}
