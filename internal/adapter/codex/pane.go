package codex

import (
	"github.com/lorem-dev/duolog/internal/core/agent"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/screen"
)

// Pane opens Codex in a peer pane.
type Pane struct{}

// Argv is the interactive command of a peer: the admitted process's launch
// argv, then the pane's own flags, none of which grants a permission. Updates
// are off through the process, in its prefix. Codex reads have only the gate,
// so the lead's mode does not change the argv.
func (Pane) Argv(r route.Route, _ mode.Mode, p corelaunch.Process, _ string) []string {
	args := p.LaunchArgv("-m", r.Model)
	if r.Effort != "" {
		args = append(args, "-c", `model_reasoning_effort="`+r.Effort+`"`)
	}
	args = append(args, "-c", "tui.notifications=false", "-c", "notify=[]")
	if r.Context == "minimal" {
		args = append(args, "-c", "project_doc_max_bytes=0")
	}
	return args
}

// Settings are the extra settings of a pane's process: none, as Codex's
// notifications are turned off in its argv.
func (Pane) Settings(agent.PaneStart) map[string]any { return nil }

// FolderTrust reads Codex's "Trust this folder?" question and gives the label
// that trusts the folder, with the folders the screen names.
func (Pane) FolderTrust(s screen.Screen) (string, []string, bool) {
	label, ok := screen.CodexFolderTrust(s)
	if !ok {
		return "", nil, false
	}
	return label, screen.CodexFolderPaths(s), true
}

// TrustsFolder reports whether Codex already trusts root in the config of the
// home that environ selects. A config that cannot be read trusts nothing.
func (Pane) TrustsFolder(root string, environ []string) bool {
	trusted, err := Trust{}.Folder(root, environ)
	return err == nil && trusted
}

// Offer reads no plugin offer: Codex asks none.
func (Pane) Offer(screen.Screen) (screen.Offer, bool) { return screen.Offer{}, false }
