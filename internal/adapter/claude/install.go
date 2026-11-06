package claude

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/lorem-dev/duolog/internal/install"
	"github.com/lorem-dev/duolog/internal/trusthome"
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
		return install.Group{Hooks: []install.Handler{h}}
	}
	wake := handler("hook wake --agent claude")
	wake.AsyncRewake = true
	return []install.Event{
		{Name: "SessionStart", Groups: []install.Group{hook("SessionStart"), {Hooks: []install.Handler{wake}}}},
		{Name: "SessionEnd", Groups: []install.Group{hook("SessionEnd")}},
		{Name: "UserPromptSubmit", Groups: []install.Group{hook("UserPromptSubmit")}},
		{Name: "PostToolUse", Groups: []install.Group{hook("PostToolUse")}},
		// A peer's turn end; a lead's Stop does nothing.
		{Name: "Stop", Groups: []install.Group{hook("Stop")}},
		{Name: "PreToolUse", Groups: []install.Group{gate("PreToolUse")}},
		{Name: "PermissionRequest", Groups: []install.Group{gate("PermissionRequest")}},
		// Only Claude's lead compacts through a hook that adds summary
		// instructions; Codex has none.
		{Name: "PreCompact", Groups: []install.Group{hook("PreCompact")}},
	}
}
