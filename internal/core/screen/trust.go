package screen

import (
	"strings"

	"github.com/lorem-dev/duolog/internal/core/pathutil"
)

// startsLine reports whether some line of text begins with marker, spaces
// aside. A marker quoted in the middle of other output does not count.
func startsLine(text, marker string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), marker) {
			return true
		}
	}
	return false
}

// folderTrust reads an agent's startup question whether to trust the folder it
// opened in: a question whose text has every marker at the start of a line and
// whose two options are the label that trusts the folder and the one that
// refuses.
func folderTrust(s Screen, markers []string, yes, no string) (string, bool) {
	if s.State != Question || len(s.Options) != 2 {
		return "", false
	}
	for _, marker := range markers {
		if !startsLine(s.Text, marker) {
			return "", false
		}
	}
	if s.Options[0].Label == yes && s.Options[1].Label == no || s.Options[0].Label == no && s.Options[1].Label == yes {
		return yes, true
	}
	return "", false
}

// folderPaths lists the absolute paths on the lines that follow the line that
// begins with heading, up to the first blank line after a path, the line that
// begins with stop, or the first line that is no path.
func folderPaths(text, heading, stop string) []string {
	var paths []string
	in := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case !in:
			in = strings.HasPrefix(line, heading)
			if rest := strings.TrimSpace(strings.TrimPrefix(line, heading)); in && pathutil.IsAbs(rest) {
				paths = append(paths, rest)
			}
		case line == "":
			if len(paths) > 0 {
				return paths
			}
		case strings.HasPrefix(line, stop):
			return paths
		// The screen shows the agent's paths, which may be POSIX or Windows
		// paths whatever this host is.
		case !pathutil.IsAbs(line):
			// A path longer than the dialog wraps onto the next line, which
			// holds no space. Anything else ends the list.
			if len(paths) == 0 || strings.ContainsAny(line, " \t") {
				return paths
			}
			paths[len(paths)-1] += line
		default:
			paths = append(paths, line)
		}
	}
	return paths
}

// trustMarkers are the texts of the agents' folder-trust screens, current and
// older ones. A question that holds one is a trust screen whatever its labels.
var trustMarkers = []string{"Accessing workspace", "Quick safety check", "Do you trust the files", "Trust this folder?", "Folder access"}

// trustReach is how many lines above the options a trust marker may start. The
// longest trust screen is about 25 lines tall.
const trustReach = 30

// startsNearOptions reports whether a line of the question block begins with
// marker: within trustReach lines above the last line that holds the first
// option's label. Output that quotes a marker further up does not count.
func startsNearOptions(s Screen, marker string) bool {
	lines := strings.Split(s.Text, "\n")
	last := len(lines) - 1
	if len(s.Options) > 0 {
		for i := len(lines) - 1; i >= 0; i-- {
			if strings.Contains(lines[i], s.Options[0].Label) {
				last = i
				break
			}
		}
	}
	for i := max(0, last-trustReach); i <= last; i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), marker) {
			return true
		}
	}
	return false
}

// OffersFolderTrust reports whether the question is a folder-trust screen: an
// option offers to trust a folder, or its text holds a marker of such a screen,
// whether or not a recogniser reads it in full.
func OffersFolderTrust(s Screen) bool {
	if s.State != Question {
		return false
	}
	for _, marker := range trustMarkers {
		if startsNearOptions(s, marker) {
			return true
		}
	}
	for _, o := range s.Options {
		label := strings.ToLower(o.Label)
		if strings.Contains(label, "trust this folder") || strings.HasPrefix(label, "trust and continue") {
			return true
		}
	}
	return false
}

// StartupName names the startup screen a question is, for a line that says
// what a peer waits on: "folder trust" or "new MCP servers" (Claude's dialog
// about the .mcp.json servers it has no decision for). It is "" for any other
// question.
func StartupName(s Screen) string {
	switch {
	case OffersFolderTrust(s):
		return "folder trust"
	case s.State == Question && (startsLine(s.Text, "New MCP server found") || startsLine(s.Text, "New MCP servers found")):
		return "new MCP servers"
	}
	return ""
}

// ClaudeFolderTrust reads Claude Code's "Accessing workspace" question and
// gives the label that trusts the folder.
func ClaudeFolderTrust(s Screen) (string, bool) {
	return folderTrust(s, []string{"Accessing workspace", "Quick safety check"}, "Yes, I trust this folder", "No, exit")
}

// ClaudeFolderPaths lists the folders that Claude's "Accessing workspace"
// question names.
func ClaudeFolderPaths(s Screen) []string {
	return folderPaths(s.Text, "Accessing workspace:", "Quick safety check")
}

// CodexFolderTrust reads Codex's "Trust this folder?" question and gives the
// label that trusts the folder.
func CodexFolderTrust(s Screen) (string, bool) {
	return folderTrust(s, []string{"Folder access", "Trust this folder?"}, "Trust and continue", "Quit")
}

// CodexFolderPaths lists the folders that Codex's "Folder access" question
// names.
func CodexFolderPaths(s Screen) []string {
	return folderPaths(s.Text, "Folder access", "Trust this folder?")
}
