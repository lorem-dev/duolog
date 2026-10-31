// Package plugin embeds the plugin files that duolog install copies.
package plugin

import "embed"

// FS holds the Claude and Codex plugin trees. The all: prefix keeps the
// hidden .claude-plugin directory.
//
//go:embed all:claude all:codex
var FS embed.FS
