// Package plugin embeds the plugin files that duolog install copies.
package plugin

import "embed"

// FS holds what duolog reads of the plugin trees: every skill, the Claude
// hooks file and its mod, and the Codex plugin. It names the Claude files one
// by one, so the mod's tests and contract, and the API types a Claude Code
// session writes into .claude-plugin/types, never reach a build.
//
//go:embed all:claude/skills claude/hooks/hooks.json claude/hooks/duolog-pane.tsx all:codex
var FS embed.FS

// Skills lists the bundled skill folders. duolog install copies them, and
// duolog sync leaves them alone.
var Skills = []string{"duolog", "duolog-skill-check", "duolog-calibrate", "duolog-peers", "duolog-update"}

// ModFile is the Claude pane mod in FS: the hooks module that
// claude/hooks/hooks.json names under "modules".
const ModFile = "claude/hooks/duolog-pane.tsx"

// ModTestedWith is the Claude Code version the mod was last tested with. The
// mod API is early access, so a later Claude Code may draw nothing.
const ModTestedWith = "2.1.319"
