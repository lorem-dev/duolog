package gate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/pelletier/go-toml/v2"
)

type LeadRules struct {
	Deny []config.Rule
	Mode mode.Mode
	// RoleSandbox is supplied from the resolved role, never from hook input.
	RoleSandbox string
}

// ImportLead reads only the orchestrator's agent settings. Settings can narrow
// the registry's live mode, but cannot raise it or remove an inherited deny.
func ImportLead(root, agent string) (LeadRules, error) {
	var rules LeadRules
	if agent != "claude" && agent != "codex" {
		return rules, fmt.Errorf("unknown lead agent %q", agent)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return rules, err
	}
	var paths []string
	if agent == "claude" {
		paths = []string{filepath.Join(home, ".claude/settings.json"), filepath.Join(root, ".claude/settings.json"), filepath.Join(root, ".claude/settings.local.json")}
	} else {
		paths = []string{filepath.Join(home, ".codex/config.toml"), filepath.Join(root, ".codex/config.toml")}
	}
	for _, path := range paths {
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
					if path == filepath.Join(home, ".claude/settings.json") {
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
			case "on-request", "untrusted", "on-failure":
				lower(mode.Ask)
			default:
				return rules, fmt.Errorf("%s: unknown approval_policy %q", path, settings.Approval)
			}
		}
	}
	return rules, nil
}

// Claude anchors // at the filesystem root, ~/ at home and / at the settings
// source (the project root for project/local settings, ~/.claude for user
// settings). Other paths use the project working directory. Hook targets use
// project-relative paths, including ../ for files outside the project.
func claudePathPattern(pattern, root, home, base string) (string, error) {
	var path string
	switch {
	case strings.HasPrefix(pattern, "//"):
		path = filepath.FromSlash(strings.TrimPrefix(pattern, "/"))
	case strings.HasPrefix(pattern, "~/"):
		path = filepath.Join(home, filepath.FromSlash(strings.TrimPrefix(pattern, "~/")))
	case strings.HasPrefix(pattern, "/"):
		path = filepath.Join(base, filepath.FromSlash(strings.TrimPrefix(pattern, "/")))
	default:
		path = filepath.Join(root, filepath.FromSlash(pattern))
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return "", fmt.Errorf("cannot rebase lead path pattern %q: %w", pattern, err)
	}
	return filepath.ToSlash(relative), nil
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
	kind := ""
	switch tool {
	case "Bash":
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
		// An unknown deny cannot be translated safely into another vendor's
		// permissions; refuse the settings instead of silently losing it.
		return config.Rule{}, fmt.Errorf("unsupported lead deny rule %q", entry)
	}
	if pattern == "" {
		return config.Rule{}, fmt.Errorf("empty deny rule %q", entry)
	}
	rule := config.Rule{Action: kind, Match: strings.TrimPrefix(pattern, "./"), Decision: Deny}
	return rule, validateRule(rule)
}
