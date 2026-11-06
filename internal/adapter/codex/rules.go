package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NativeAllow writes Codex's allow rules into the project's
// .codex/rules/duolog.rules.
type NativeAllow struct{}

// Allow appends a prefix_rule for a command that ends in " *". A Codex prefix
// rule also matches extra arguments and runs them outside the sandbox, so an
// exact command gets no Codex rule.
func (NativeAllow) Allow(root, match string) (string, error) {
	prefix, anyArgs := nativePrefix(match)
	if !anyArgs {
		return "no rule; Codex rules match a prefix, and this command is exact", nil
	}
	rule, err := addRule(filepath.Join(root, ".codex", "rules", "duolog.rules"), prefix)
	if err != nil {
		return "", err
	}
	return rule + " (outside the sandbox, trusted project only)", nil
}

// AllowMCP writes nothing: Codex approves MCP tools per server in config.toml,
// and an entry there for a server the project does not define would add a
// broken server.
func (NativeAllow) AllowMCP(_, _, _ string) (string, error) {
	return "no rule; Codex sets MCP tool approval per server in its own config.toml", nil
}

// nativePrefix turns a remembered glob into the command prefix the agents match:
// "ruff check *" is the prefix "ruff check"; a glob with no trailing " *" is exact.
func nativePrefix(match string) (prefix string, anyArgs bool) {
	if strings.HasSuffix(match, " *") {
		return strings.TrimSuffix(match, " *"), true
	}
	return match, false
}

// addRule appends a prefix_rule to the project's Codex rules. Codex reads
// <repo>/.codex/rules/*.rules only in a trusted project, and an allowed prefix
// runs outside the sandbox without a prompt, as Codex's own approvals do.
func addRule(path, prefix string) (string, error) {
	quoted := make([]string, 0, 4)
	for _, word := range strings.Fields(prefix) {
		quoted = append(quoted, fmt.Sprintf("%q", word))
	}
	rule := fmt.Sprintf("prefix_rule(pattern = [%s], decision = \"allow\", justification = \"remembered with duolog remember\")\n", strings.Join(quoted, ", "))
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if strings.Contains(string(data), rule) {
		return "already " + strings.TrimSpace(rule), nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	_, err = f.WriteString(rule)
	return strings.TrimSpace(rule), err
}
