// Package caps holds what an agent has (skills, plugins, MCP servers), the
// decision of what to do when a worker lacks one, and the rules for naming MCP
// capabilities. It reads no file: the agents' adapters read their lists.
package caps

import "regexp"

// ClaudeNameChars matches what Claude does not allow in the server or tool
// part of an MCP tool name.
var ClaudeNameChars = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// ClaudeName is s as Claude spells it in mcp__<server>__<tool>.
func ClaudeName(s string) string { return ClaudeNameChars.ReplaceAllString(s, "_") }
