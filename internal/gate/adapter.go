package gate

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// Normalize accepts the shared hook envelope, not model-facing tool names.
func Normalize(agent string, input []byte) (Action, error) {
	if agent != "claude" && agent != "codex" {
		return Action{}, fmt.Errorf("unknown agent %q", agent)
	}
	var hook struct {
		Tool  string `json:"tool_name"`
		Cwd   string `json:"cwd"`
		Input struct {
			Command string `json:"command"`
			Path    string `json:"file_path"`
			URL     string `json:"url"`
			Query   string `json:"query"`
		} `json:"tool_input"`
	}
	if err := json.Unmarshal(input, &hook); err != nil {
		return Action{}, err
	}
	// Only Bash and spawn have verified Codex hook names. Claude tool names
	// cannot confer permissions on an unverified Codex tool.
	if agent == "codex" && hook.Tool != "" && hook.Tool != "Bash" && hook.Tool != "collaborationspawn_agent" && hook.Tool != "spawn_agent" && hook.Tool != "apply_patch" {
		return Action{Kind: "unknown", Target: hook.Tool}, nil
	}
	a := Action{}
	switch hook.Tool {
	case "Bash":
		a.Kind, a.Target = "exec", strings.TrimSpace(hook.Input.Command)
	case "Read", "Write", "Edit", "MultiEdit":
		a.Kind = "write"
		if hook.Tool == "Read" {
			a.Kind = "read"
		}
		if hook.Input.Path != "" {
			a.Target = cleanPath(hook.Input.Path)
			if filepath.IsAbs(a.Target) && hook.Cwd != "" {
				if relative, err := filepath.Rel(hook.Cwd, a.Target); err == nil {
					a.Target = cleanPath(relative)
				}
			}
		}
	case "apply_patch":
		// Its hook name and target representation were not verified. An empty
		// target records a write but always requires approval.
		a.Kind = "write"
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
