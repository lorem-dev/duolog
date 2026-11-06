package install

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/trusthome"
)

// HookGroup is one entry of a settings or hooks file, as both agents write it.
type HookGroup struct {
	Matcher string `json:"matcher"`
	Hooks   []struct {
		Type           string   `json:"type"`
		Command        string   `json:"command"`
		CommandWindows string   `json:"commandWindows"`
		Args           []string `json:"args"`
		// Async and AsyncRewake run a Claude hook in the background: Claude
		// does not wait for its decision. Timeout is in seconds; nil leaves
		// the agent's default.
		Async       bool     `json:"async"`
		AsyncRewake bool     `json:"asyncRewake"`
		Timeout     *float64 `json:"timeout"`
	} `json:"hooks"`
}

// GateTimeout is the timeout, in seconds, that install writes for a gate
// hook: longer than the gate's longest wait.
const GateTimeout = 150

// GateWaits reports whether Claude waits for handler i's decision as long
// as the gate may need: it is not async and its timeout, if set, is at least
// GateTimeout. Otherwise it says why not.
func (g HookGroup) GateWaits(i int) (string, bool) {
	h := g.Hooks[i]
	switch {
	case h.Async || h.AsyncRewake:
		return "it runs in the background (async), and Claude does not wait for its decision", false
	case h.Timeout != nil && *h.Timeout < GateTimeout:
		return fmt.Sprintf("its timeout of %g seconds is below the %d that duolog install writes, and Claude runs the tool when a hook times out", *h.Timeout, GateTimeout), false
	}
	return "", true
}

// ReadHooks returns the hooks of a settings file. A missing or broken file has none.
func ReadHooks(path string) map[string][]HookGroup {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc struct {
		Hooks map[string][]HookGroup `json:"hooks"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return nil
	}
	return doc.Hooks
}

// CoversEveryTool reports whether the group runs for each tool. A gate behind a
// narrower matcher lets the other tools through with no decision.
func (g HookGroup) CoversEveryTool() bool {
	return g.Matcher == "" || g.Matcher == "*" || g.Matcher == ".*"
}

// IsGate reports whether handler i of the group runs the permission gate. Another
// duolog subcommand, such as a hook, decides nothing.
func (g HookGroup) IsGate(i int) bool {
	h := g.Hooks[i]
	for _, command := range []string{strings.Join(append([]string{h.Command}, h.Args...), " "), h.CommandWindows} {
		if IsOurCommand(command) && strings.Contains(command, "duolog gate") {
			return true
		}
	}
	return false
}

// GateProgram returns the program of handler i when it is exactly the gate
// that duolog install writes for agent and event: a program named duolog,
// then gate --agent <agent> --event <event>, as a command with args or as one
// command of plain words, with no comment or other shell text. IsGate only
// recognises the gate's name, so it also accepts a hook that never runs it.
func (g HookGroup) GateProgram(i int, agent, event string) (string, bool) {
	h := g.Hooks[i]
	if h.Type != "command" {
		return "", false
	}
	program, args := h.Command, h.Args
	if len(args) == 0 {
		if strings.ContainsAny(h.Command, "#;&|$`'\"<>(){}[]*?~!\\=%^\n\r") {
			return "", false
		}
		words := strings.Fields(h.Command)
		if len(words) == 0 {
			return "", false
		}
		program, args = words[0], words[1:]
	}
	if program == "" || strings.ContainsAny(program, "\n\r") || !slices.Equal(args, []string{"gate", "--agent", agent, "--event", event}) {
		return "", false
	}
	base := program[strings.LastIndexAny(program, `/\`)+1:]
	if name := strings.ToLower(base); name != "duolog" && name != "duolog.exe" {
		return "", false
	}
	return program, true
}

// devHookPrefix starts the gate hook of duolog's own .claude/settings.json,
// which runs the dev launcher of the checkout (docs/contributing.md).
const devHookPrefix = `exec "$CLAUDE_PROJECT_DIR"/scripts/dev/bin/duolog `

// devLauncherHead is how duolog's dev launcher, scripts/dev/bin/duolog,
// starts.
const devLauncherHead = "#!/bin/sh\n# duolog built from this checkout."

// DevGateLauncher returns the dev launcher under root that handler i runs
// when it is exactly the dev form of the gate for agent and event: exec
// "$CLAUDE_PROJECT_DIR"/scripts/dev/bin/duolog gate --agent <agent> --event
// <event>, as one command with no args and no other shell text, the variable
// read as root. Whether the file is the launcher is IsDevLauncher's to say.
func (g HookGroup) DevGateLauncher(i int, agent, event, root string) (string, bool) {
	h := g.Hooks[i]
	if h.Type != "command" || len(h.Args) != 0 || root == "" || h.Command != devHookPrefix+"gate --agent "+agent+" --event "+event {
		return "", false
	}
	return filepath.Join(root, "scripts", "dev", "bin", "duolog"), true
}

// IsDevLauncher reports whether path is a regular file that starts as
// duolog's dev launcher does, in a checkout with either line ending.
func IsDevLauncher(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, len(devLauncherHead)+1)
	n, _ := io.ReadFull(f, head)
	return strings.HasPrefix(strings.ReplaceAll(string(head[:n]), "\r\n", "\n"), devLauncherHead)
}

// IsOurs reports whether handler i of the group is duolog's.
func (g HookGroup) IsOurs(i int) bool {
	h := g.Hooks[i]
	return IsOurCommand(h.Command) || IsOurCommand(h.CommandWindows)
}

// HasGate reports whether the file has a permission gate that applies to every tool.
func HasGate(path string) bool {
	for _, group := range ReadHooks(path)["PreToolUse"] {
		for i := range group.Hooks {
			if group.IsGate(i) && group.CoversEveryTool() {
				return true
			}
		}
	}
	return false
}

// HasOurHook reports whether any event in the file runs duolog.
func HasOurHook(path string) bool {
	for _, groups := range ReadHooks(path) {
		for _, group := range groups {
			for i := range group.Hooks {
				if group.IsOurs(i) {
					return true
				}
			}
		}
	}
	return false
}

// ClaudeSettingsFiles lists the files Claude reads, in the order it applies them.
func ClaudeSettingsFiles(root, home string) []string {
	return ClaudeSettingsFilesIn(root, home, os.Environ())
}

// ClaudeSettingsFilesIn lists the files a Claude process whose environment is
// environ reads, in the order it applies them.
func ClaudeSettingsFilesIn(root, home string, environ []string) []string {
	paths := []string{}
	if home != "" {
		paths = append(paths, filepath.Join(trusthome.ClaudeIn(home, environ), "settings.json"))
	}
	if root != "" && root != home {
		paths = append(paths, filepath.Join(root, ".claude", "settings.json"), filepath.Join(root, ".claude", "settings.local.json"))
	}
	return paths
}

// PluginInstalled reports whether Claude lists the duolog plugin as installed
// under home for the project root, in a folder whose hooks.json holds the
// gate. An enabled plugin that is not installed runs no hook. An install at
// user scope counts everywhere; a project or local install only for its own
// projectPath; any other scope does not count.
func PluginInstalled(root, home string) bool {
	return PluginInstalledIn(root, home, os.Environ())
}

// PluginInstalledIn is PluginInstalled for a Claude process whose
// environment is environ.
func PluginInstalledIn(root, home string, environ []string) bool {
	return len(PluginGateFilesIn(root, home, environ)) > 0
}

// PluginGateFilesIn lists the hooks.json files of the duolog plugin installs
// that count for root, as PluginInstalledIn counts them, whose gate applies
// to every tool.
func PluginGateFilesIn(root, home string, environ []string) []string {
	data, err := os.ReadFile(filepath.Join(trusthome.ClaudeIn(home, environ), "plugins", "installed_plugins.json"))
	if err != nil {
		return nil
	}
	var doc struct {
		Plugins map[string][]struct {
			Scope       string `json:"scope"`
			ProjectPath string `json:"projectPath"`
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return nil
	}
	if abs, err := filepath.Abs(root); err == nil && root != "" {
		root = abs
	}
	var files []string
	for _, in := range doc.Plugins[pluginKey] {
		switch in.Scope {
		case "", "user":
		case "project", "local":
			if in.ProjectPath == "" || root == "" || kit.Canonical(in.ProjectPath) != kit.Canonical(root) {
				continue
			}
		default:
			continue
		}
		if path := filepath.Join(in.InstallPath, "hooks", "hooks.json"); in.InstallPath != "" && HasGate(path) {
			files = append(files, path)
		}
	}
	return files
}

// PluginEnabled applies project and local plugin settings after user settings. An
// explicit project disable overrides an enabled user plugin.
func PluginEnabled(root, home string) bool {
	return PluginEnabledIn(root, home, os.Environ())
}

// PluginEnabledIn is PluginEnabled for a Claude process whose environment is
// environ.
func PluginEnabledIn(root, home string, environ []string) bool {
	enabled := false
	for _, path := range ClaudeSettingsFilesIn(root, home, environ) {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var doc struct {
			Enabled map[string]bool `json:"enabledPlugins"`
		}
		if json.Unmarshal(data, &doc) == nil {
			if value, ok := doc.Enabled[pluginKey]; ok {
				enabled = value
			}
		}
	}
	return enabled
}
