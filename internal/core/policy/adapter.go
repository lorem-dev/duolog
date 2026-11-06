package policy

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/caps"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
)

// Normalize accepts the shared hook envelope, not model-facing tool names.
func Normalize(agent string, input []byte) (Action, error) {
	if agent != "claude" && agent != "codex" {
		return Action{}, fmt.Errorf("unknown agent %q", agent)
	}
	var hook struct {
		Tool string `json:"tool_name"`
		Cwd  string `json:"cwd"`
		// Claude also names the server as configured, which may hold __.
		Server struct {
			Name string `json:"name"`
		} `json:"mcp_server"`
		Input struct {
			Command    string `json:"command"`
			Path       string `json:"file_path"`
			SearchPath string `json:"path"`
			Notebook   string `json:"notebook_path"`
			Patch      string `json:"patch"`
			URL        string `json:"url"`
			Query      string `json:"query"`
			Glob       string `json:"glob"`
		} `json:"tool_input"`
	}
	if err := json.Unmarshal(input, &hook); err != nil {
		return Action{}, err
	}
	// Accept known Codex hook names, including the native Windows shell tool.
	// Other Claude tool names cannot confer permissions on an unknown Codex tool.
	// Both agents name an MCP tool mcp__<server>__<tool> (checked live with
	// Codex 0.165.0 and Claude Code 2.1.316); Codex writes - in a server name as _.
	if target, ok := MCPTarget(hook.Tool); ok {
		if name := hook.Server.Name; name != "" {
			prefix := "mcp__" + claudeNameChars.ReplaceAllString(name, "_") + "__"
			if tool, ok := strings.CutPrefix(hook.Tool, prefix); ok && tool != "" {
				return Action{Kind: "mcp", Target: name + "/" + tool, MCPExact: true}, nil
			}
		}
		return Action{Kind: "mcp", Target: target}, nil
	}
	if agent == "codex" && hook.Tool != "" && hook.Tool != "Bash" && hook.Tool != "PowerShell" && hook.Tool != "collaborationspawn_agent" && hook.Tool != "spawn_agent" && hook.Tool != "apply_patch" {
		return Action{Kind: "unknown", Target: hook.Tool}, nil
	}
	a := Action{}
	switch hook.Tool {
	case "Bash", "PowerShell":
		a.Kind, a.Target = "exec", strings.TrimSpace(hook.Input.Command)
		if hook.Tool == "PowerShell" {
			a.Shell = "powershell"
		}
	case "Read", "Grep", "Glob", "LS", "Write", "Edit", "MultiEdit", "NotebookEdit":
		a.Kind = "write"
		if hook.Tool == "Read" || hook.Tool == "Grep" || hook.Tool == "Glob" || hook.Tool == "LS" {
			a.Kind = "read"
		}
		if hook.Tool == "Grep" {
			a.Search, a.Glob = true, hook.Input.Glob
		}
		if hook.Input.Path == "" {
			hook.Input.Path = hook.Input.SearchPath
		}
		if hook.Input.Path == "" {
			hook.Input.Path = hook.Input.Notebook
		}
		if hook.Input.Path == "" && a.Kind == "read" && hook.Tool != "Read" {
			hook.Input.Path = "."
		}
		if hook.Input.Path != "" {
			if pathutil.Windows(hook.Input.Path) || pathutil.Windows(hook.Cwd) {
				a.Shell = "windows"
			}
			a.Target = cleanPath(hook.Input.Path)
			if pathutil.IsAbs(a.Target) && hook.Cwd != "" {
				if relative, err := pathutil.Relative(hook.Cwd, a.Target); err == nil {
					a.Target = cleanPath(relative)
				}
			}
		}
	case "apply_patch":
		a.Kind = "write"
		paths := PatchPaths(hook.Input.Patch + "\n" + hook.Input.Command)
		if len(paths) == 1 {
			if pathutil.Windows(paths[0]) || pathutil.Windows(hook.Cwd) {
				a.Shell = "windows"
			}
			a.Target = cleanPath(paths[0])
		}
	case "TodoWrite", "Skill", "ToolSearch", "TaskStop", "TaskOutput":
		// ToolSearch only loads the definition of a deferred tool, such as an
		// MCP tool; calling the tool is checked on its own. TaskStop and
		// TaskOutput stop or read the worker's own background task.
		a.Kind, a.Target = "internal", hook.Tool
	case "StructuredOutput":
		// Claude's own tool for returning the schema answer; without it a worker
		// cannot finish and delegate reports "unexpected EOF".
		a.Kind, a.Target = "answer", hook.Tool
	case "Task", "Agent", "collaborationspawn_agent", "spawn_agent":
		a.Kind, a.Target = "spawn", hook.Tool
	case "WebFetch", "WebSearch":
		a.Kind, a.Target = "net", hook.Input.URL
		if a.Target == "" {
			a.Target = hook.Input.Query
		}
	case "":
		return Action{}, fmt.Errorf("hook is missing tool_name")
	default:
		a.Kind, a.Target = "unknown", hook.Tool
	}
	if a.Target == "" && hook.Tool != "apply_patch" {
		return Action{}, fmt.Errorf("%s hook is missing its target", hook.Tool)
	}
	return a, nil
}

// Render only emits decisions in formats observed to be honored. An unresolved
// Ask fails closed; hook callers resolve it through Escalate before rendering.
func Render(agent, event string, d Decision) []byte {
	if d.Kind == Allow && event == "PreToolUse" {
		return nil
	}
	kind := d.Kind
	if kind != Allow {
		kind = Deny
	}
	output := map[string]any{"hookEventName": event}
	if event == "PermissionRequest" {
		decision := map[string]string{"behavior": kind}
		if kind == Deny {
			decision["message"] = d.Reason
		}
		output["decision"] = decision
	} else {
		output["permissionDecision"] = kind
		output["permissionDecisionReason"] = d.Reason
	}
	data, _ := json.Marshal(map[string]any{"hookSpecificOutput": output})
	return append(data, '\n')
}

// RenderLead asks the person in Claude and directs Codex users to their terminal.
func RenderLead(agent string, d Decision) []byte {
	kind := Ask
	switch agent {
	case "claude":
	case "codex":
		kind = Deny
	default:
		return nil
	}
	data, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{
		"hookEventName":            "PreToolUse",
		"permissionDecision":       kind,
		"permissionDecisionReason": d.Reason,
	}})
	return append(data, '\n')
}

// PatchPaths covers every modified path, including both sides of a move.
// The apply_patch hook name remains unverified in the saved live evidence.
func PatchPaths(patch string) []string {
	var paths []string
	for _, line := range strings.Split(patch, "\n") {
		for _, prefix := range []string{"*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: "} {
			if path, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok && path != "" {
				paths = append(paths, path)
			}
		}
	}
	return paths
}

func NormalizeAll(agent string, input []byte) ([]Action, error) {
	a, err := Normalize(agent, input)
	if err != nil {
		return nil, err
	}
	var h struct {
		Tool  string                                         `json:"tool_name"`
		Cwd   string                                         `json:"cwd"`
		Input struct{ Patch, Command, Pattern, Glob string } `json:"tool_input"`
	}
	if json.Unmarshal(input, &h) != nil {
		return []Action{a}, nil //nolint:nilerr // unparsable input keeps the action built so far, and the gate decides on it
	}
	// A search names what it reads in its pattern as well as in its path.
	if h.Tool == "Glob" || h.Tool == "Grep" {
		pattern := h.Input.Glob
		if h.Tool == "Glob" {
			pattern = h.Input.Pattern
		}
		actions := []Action{a}
		if pattern != "" {
			actions = append(actions, Action{Kind: "read", Target: cleanPath(pattern), Shell: a.Shell})
		}
		return actions, nil
	}
	if h.Tool != "apply_patch" {
		return []Action{a}, nil
	}
	paths := PatchPaths(h.Input.Patch + "\n" + h.Input.Command)
	if len(paths) == 0 {
		return []Action{a}, nil
	}
	actions := make([]Action, 0, len(paths))
	for _, path := range paths {
		action := Action{Kind: "write", Target: cleanPath(path)}
		if pathutil.Windows(path) || pathutil.Windows(h.Cwd) {
			action.Shell = "windows"
		}
		actions = append(actions, action)
	}
	return actions, nil
}

var claudeNameChars = caps.ClaudeNameChars

// MCPTarget turns mcp__<server>__<tool> into <server>/<tool>. The server is
// what comes before the first __ after the prefix.
func MCPTarget(tool string) (string, bool) {
	rest, ok := strings.CutPrefix(tool, "mcp__")
	if !ok {
		return "", false
	}
	server, name, ok := strings.Cut(rest, "__")
	if !ok || server == "" || name == "" {
		return "", false
	}
	return server + "/" + name, true
}

// MCPName folds a server or tool name the way the agents do in tool names:
// Claude turns characters other than letters, digits, _ and - into _, and
// Codex turns - into _ as well. Rules and actions compare in this form.
func MCPName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '*' || r == '/' || r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
