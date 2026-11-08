package claude

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lorem-dev/duolog/internal/install"
	"github.com/lorem-dev/duolog/internal/trusthome"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// Installer writes duolog's hooks and skill for Claude Code: the hooks in
// .claude/settings.json, the skill in .claude/skills, and the install offer
// in CLAUDE.md. Its Enabled method makes it a Plugins too: the duolog Claude
// plugin registers the same hooks and skill.
type Installer struct{}

// HookFile is the settings file whose hooks scope selects: user scope is the
// Claude config folder under home (CLAUDE_CONFIG_DIR applies), project scope
// .claude under base.
func (Installer) HookFile(scope, base, home string) string {
	if scope == "user" {
		return filepath.Join(trusthome.Claude(home), "settings.json")
	}
	return filepath.Join(base, ".claude", "settings.json")
}

// SkillDir is the folder the bundled skills go into.
func (Installer) SkillDir(scope, base, home string) string {
	if scope == "user" {
		return filepath.Join(trusthome.Claude(base), "skills")
	}
	return filepath.Join(base, ".claude", "skills")
}

// Hooks merges duolog's Claude hooks, with gate timeouts of timeout seconds,
// into the settings file whose bytes are existing (nil for none). A timeout
// of zero removes duolog's hooks instead. The notes are the steps the person
// still has to do: none for Claude.
func (Installer) Hooks(existing []byte, timeout int) ([]byte, []string, error) {
	var ours []byte
	if timeout != 0 {
		var err error
		if ours, err = HooksJSON(timeout); err != nil {
			return nil, nil, err
		}
	}
	data, err := install.Merge(existing, ours)
	return data, nil, err
}

// OurHooks returns duolog's own hooks in the settings file whose bytes are
// data, as install.OurHooks reads them.
func (Installer) OurHooks(data []byte) (map[string][]string, error) { return install.OurHooks(data) }

// DevHooks reports whether duolog's hooks in data run the dev launcher of the
// duolog checkout at root.
func (Installer) DevHooks(data []byte, root string) bool { return install.RunsDevLauncher(data, root) }

// InstructionFiles names the instruction file Claude reads in the project at
// root, the existing .claude/CLAUDE.md or else CLAUDE.md, and every place one
// could have been written.
func (Installer) InstructionFiles(root string) (current string, all []string) {
	current = "CLAUDE.md"
	if _, err := os.Stat(filepath.Join(root, ".claude", "CLAUDE.md")); err == nil {
		current = filepath.Join(".claude", "CLAUDE.md")
	}
	return filepath.Join(root, current), []string{filepath.Join(root, "CLAUDE.md"), filepath.Join(root, ".claude", "CLAUDE.md")}
}

// LocalSettings is the project's local settings file, .claude/settings.local.json
// under root, which Claude keeps out of Git and which applies over the user
// and project settings. Install writes the lead's compact window there.
func (Installer) LocalSettings(root string) string {
	return filepath.Join(root, ".claude", "settings.local.json")
}

// OtherSettings lists every other settings file a Claude lead at root reads,
// as workerenv.SettingsLayers finds them for home and environ, from the lowest
// precedence to the highest: the user file, the project and local files of
// root and the folders above it, and managed policy.
func (i Installer) OtherSettings(root, home string, environ []string) []string {
	local := filepath.Clean(i.LocalSettings(root))
	var paths []string
	for _, layer := range workerenv.SettingsLayers("claude", workerenv.Locations{Home: home, Root: root, GOOS: runtime.GOOS, Environ: environ}) {
		if layer.Path != "" && filepath.Clean(layer.Path) != local {
			paths = append(paths, layer.Path)
		}
	}
	return paths
}

// ServesMCP says Claude's lead gets the duolog MCP server, so its instruction
// block carries the MCP line. Task 10's install.MCPServers replaces it.
func (Installer) ServesMCP() bool { return true }

// Enabled reports whether the duolog plugin is enabled for a Claude process
// whose environment is environ: project and local settings apply after user
// settings, and an explicit project disable overrides an enabled user plugin.
func (Installer) Enabled(root, home string, environ []string) (bool, error) {
	return install.PluginEnabledIn(root, home, environ), nil
}

// HooksJSON renders the hooks document of Claude, as the plugin ships it.
func HooksJSON(timeout int) ([]byte, error) {
	return install.Document(events(timeout))
}

// events returns the hook entries of Claude in a fixed order. Timeouts are in
// seconds.
func events(timeout int) []install.Event {
	handler := func(args string) install.Handler {
		return install.Handler{Type: "command", Command: "duolog", Args: strings.Fields(args)}
	}
	hook := func(name string) install.Group {
		return install.Group{Hooks: []install.Handler{handler("hook " + name + " --agent claude")}}
	}
	gate := func(name string) install.Group {
		h := handler("gate --agent claude --event " + name)
		h.Timeout = timeout
		// Without it Claude lets the tool run when it kills the hook.
		h.OnFailure = "block"
		return install.Group{Hooks: []install.Handler{h}}
	}
	wake := handler("hook wake --agent claude")
	wake.AsyncRewake = true
	return []install.Event{
		{Name: "SessionStart", Groups: []install.Group{hook("SessionStart"), {Hooks: []install.Handler{wake}}}},
		{Name: "SessionEnd", Groups: []install.Group{hook("SessionEnd")}},
		{Name: "UserPromptSubmit", Groups: []install.Group{hook("UserPromptSubmit")}},
		{Name: "PostToolUse", Groups: []install.Group{hook("PostToolUse")}},
		// A peer's turn end; a lead must keep a wait for its busy peers.
		{Name: "Stop", Groups: []install.Group{hook("Stop")}},
		{Name: "PreToolUse", Groups: []install.Group{gate("PreToolUse")}},
		{Name: "PermissionRequest", Groups: []install.Group{gate("PermissionRequest")}},
		// Only Claude's lead compacts through a hook that adds summary
		// instructions; Codex has none.
		{Name: "PreCompact", Groups: []install.Group{hook("PreCompact")}},
	}
}
