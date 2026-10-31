// Package install writes duolog hooks, skills and a starter config for Claude
// Code and Codex, at user or project scope. It only touches paths under
// Options.Home and Options.Root and never the process home directory.
package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/trusthome"
	"github.com/lorem-dev/duolog/plugin"
)

// Options selects what to install and where. Scope is "project" (default) or
// "user"; Agent is "claude", "codex" or "both" (default).
type Options struct {
	Scope, Agent string
	Home, Root   string
}

// Report lists the files written and the steps the user still has to do.
type Report struct {
	Files []string
	Notes []string
}

const backupSuffix = ".duolog.bak"

// edit is one planned file write. Nothing is written until every edit is built.
type edit struct {
	path string
	data []byte
}

// Run installs hooks, the skill and, in project scope, a starter config.
func Run(o Options) (Report, error) {
	agents, base, err := resolve(o)
	if err != nil {
		return Report{}, err
	}
	timeout := 150
	var edits []edit
	var report Report
	for _, agent := range agents {
		path := hooksPath(base, agent)
		if o.Scope == "user" {
			path = userHooksPath(base, agent)
		}
		if agent == "claude" && pluginEnabled(base, o.Home) {
			if runtime.GOOS == "windows" {
				return Report{}, fmt.Errorf("disable the duolog Claude plugin before installing native Windows hooks; its Bash hooks require Git Bash")
			}
			// The plugin already registers these hooks and the skill; a second
			// copy in settings.json would run every gate twice. Drop any copy an
			// earlier install left behind.
			if _, err := os.Stat(path); err == nil {
				data, err := mergeHooks(path, nil)
				if err != nil {
					return Report{}, err
				}
				edits = append(edits, edit{path, data})
			}
			report.Notes = append(report.Notes, "Claude: the "+pluginKey+" plugin is enabled, so its hooks and skill are used and none were installed here. Gate waits are capped at 120 seconds.")
			continue
		}
		hooks, err := HooksJSON(agent, runtime.GOOS, timeout)
		if err != nil {
			return Report{}, err
		}
		data, err := mergeHooks(path, hooks)
		if err != nil {
			return Report{}, err
		}
		skill, err := plugin.FS.ReadFile(agent + "/skills/duolog/SKILL.md")
		if err != nil {
			return Report{}, err
		}
		edits = append(edits, edit{path, data}, edit{installedSkillPath(o, base, agent), skill})
	}
	if o.Scope != "user" {
		if _, err := store.Open(o.Root); err != nil {
			return Report{}, err
		}
		instructions, err := instructionEdits(o.Root)
		if err != nil {
			return Report{}, err
		}
		edits = append(edits, instructions...)
		path := filepath.Join(o.Root, ".duolog", "config.yaml")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			edits = append(edits, edit{path, []byte("# yaml-language-server: $schema=" + config.SchemaURL + "\nversion: 1\n")})
		} else if err != nil {
			return Report{}, err
		}
	}
	for _, e := range edits {
		changed, err := apply(e)
		if err != nil {
			return report, err
		}
		if changed {
			report.Files = append(report.Files, e.path)
		}
	}
	for _, agent := range agents {
		if agent == "codex" {
			report.Notes = append(report.Notes, "Codex: approve the duolog hooks in /hooks (press t), and trust this project, or Codex will not run them.")
		}
	}
	return report, nil
}

// Remove deletes only what Run added: our hook entries and our skill file.
// Foreign hooks, the config file and everything else stay.
func Remove(o Options) error {
	agents, base, err := resolve(o)
	if err != nil {
		return err
	}
	var edits []edit
	for _, agent := range agents {
		path := hooksPath(base, agent)
		if o.Scope == "user" {
			path = userHooksPath(base, agent)
		}
		if _, err := os.Stat(path); err == nil {
			data, err := mergeHooks(path, nil)
			if err != nil {
				return err
			}
			edits = append(edits, edit{path, data})
		}
	}
	for _, e := range edits {
		if _, err := apply(e); err != nil {
			return err
		}
	}
	for _, agent := range agents {
		skill := installedSkillPath(o, base, agent)
		if err := os.Remove(skill); err != nil && !os.IsNotExist(err) {
			return err
		}
		_ = os.Remove(filepath.Dir(skill)) // only succeeds when empty
	}
	if o.Scope != "user" {
		return removeInstructions(o.Root)
	}
	return nil
}

// pluginKey is the enabledPlugins key of the duolog Claude plugin.
const pluginKey = "duolog@duolog"

// pluginEnabled applies project and local plugin settings after user settings.
// An explicit project disable overrides an enabled user plugin.
func pluginEnabled(base, home string) bool {
	paths := []string{}
	if home != "" {
		paths = append(paths, filepath.Join(trusthome.Claude(home), "settings.json"))
	}
	if base != "" && base != home {
		paths = append(paths, filepath.Join(base, ".claude", "settings.json"), filepath.Join(base, ".claude", "settings.local.json"))
	}
	enabled := false
	for _, path := range paths {
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

func resolve(o Options) (agents []string, base string, err error) {
	switch o.Agent {
	case "", "both":
		agents = []string{"claude", "codex"}
	case "claude", "codex":
		agents = []string{o.Agent}
	default:
		return nil, "", fmt.Errorf("unknown agent %q: use claude, codex or both", o.Agent)
	}
	switch o.Scope {
	case "user":
		base = o.Home
	case "", "project":
		base = o.Root
	default:
		return nil, "", fmt.Errorf("unknown scope %q: use user or project", o.Scope)
	}
	if base == "" {
		return nil, "", fmt.Errorf("no directory for scope %q", o.Scope)
	}
	return agents, base, nil
}

func hooksPath(base, agent string) string {
	if agent == "claude" {
		return filepath.Join(base, ".claude", "settings.json")
	}
	return filepath.Join(base, ".codex", "hooks.json")
}

func userHooksPath(home, agent string) string {
	if agent == "claude" {
		return filepath.Join(trusthome.Claude(home), "settings.json")
	}
	return filepath.Join(trusthome.Codex(home), "hooks.json")
}
func installedSkillPath(o Options, base, agent string) string {
	if o.Scope == "user" && agent == "claude" {
		return filepath.Join(trusthome.Claude(base), "skills", "duolog", "SKILL.md")
	}
	return skillPath(base, agent)
}

func skillPath(base, agent string) string {
	if agent == "claude" {
		return filepath.Join(base, ".claude", "skills", "duolog", "SKILL.md")
	}
	return filepath.Join(base, ".agents", "skills", "duolog", "SKILL.md")
}

// apply writes e when its bytes differ from the file. An existing file is
// copied to <file>.duolog.bak first; an older backup is kept as the original.
func apply(e edit) (bool, error) {
	old, err := os.ReadFile(e.path)
	if err == nil && bytes.Equal(old, e.data) {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(e.path), 0o755); err != nil {
		return false, err
	}
	if err == nil {
		if _, statErr := os.Stat(e.path + backupSuffix); os.IsNotExist(statErr) {
			if err := os.WriteFile(e.path+backupSuffix, old, 0o644); err != nil {
				return false, err
			}
		}
	}
	tmp := e.path + ".duolog.tmp"
	if err := os.WriteFile(tmp, e.data, 0o644); err != nil {
		return false, err
	}
	if err := store.Rename(tmp, e.path); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	return true, nil
}

type handler struct {
	Type           string `json:"type"`
	Command        string `json:"command"`
	CommandWindows string `json:"commandWindows,omitempty"`
	Shell          string `json:"shell,omitempty"`
	Timeout        int    `json:"timeout,omitempty"`
	AsyncRewake    bool   `json:"asyncRewake,omitempty"`
}

type group struct {
	Hooks []handler `json:"hooks"`
}

type event struct {
	name   string
	groups []group
}

// events returns the hook entries of one agent in a fixed order. Timeouts are
// in seconds for Claude; Codex hooks.json also takes seconds.
func events(agent, goos string, timeout int) []event {
	makeHandler := func(args string, gate bool) handler {
		h := handler{Type: "command", Command: command("linux", agent, args, gate)}
		if agent == "codex" {
			h.CommandWindows = command("windows", agent, args, gate)
		} else {
			h.Shell = "bash"
			if goos == "windows" {
				h.Command = powershellGuard(args, gate)
				h.Shell = "powershell"
			}
		}
		return h
	}
	hook := func(name string) group {
		return group{[]handler{makeHandler("hook "+name+" --agent "+agent, false)}}
	}
	gate := func(name string) group {
		h := makeHandler("gate --agent "+agent+" --event "+name, true)
		h.Timeout = timeout
		return group{[]handler{h}}
	}
	start := []group{hook("SessionStart")}
	if agent == "claude" {
		h := makeHandler("hook wake --agent claude", false)
		h.AsyncRewake = true
		start = append(start, group{[]handler{h}})
	}
	return []event{
		{"SessionStart", start},
		{"SessionEnd", []group{hook("SessionEnd")}},
		{"UserPromptSubmit", []group{hook("UserPromptSubmit")}},
		{"PostToolUse", []group{hook("PostToolUse")}},
		{"PreToolUse", []group{gate("PreToolUse")}},
		{"PermissionRequest", []group{gate("PermissionRequest")}},
	}
}

// An exec-form hook cannot make a missing executable silent for leads while
// blocking workers. Claude therefore needs an explicit per-OS shell guard;
// its format has no Windows override. Codex carries both forms in one file.
// POSIX must exec so registration records the agent, not a short-lived shell.
func command(goos, agent, args string, gate bool) string {
	if goos == "windows" {
		body := powershellGuard(args, gate)
		if agent == "claude" {
			return body
		}
		// No variable expansion or embedded double quotes: safe when the outer
		// Windows shell is PowerShell, Git Bash or cmd. Codex documents no shell.
		return `powershell -NoProfile -NonInteractive -Command "` + body + `"`
	}
	s := "if command -v duolog >/dev/null 2>&1; then exec duolog " + args + "; "
	if gate {
		s += `elif [ "${DUOLOG_DEPTH:-0}" -ge 1 ] 2>/dev/null; then exit 2; `
	}
	return s + "fi"
}

func powershellGuard(args string, gate bool) string {
	s := "if (Get-Command duolog -CommandType Application -ErrorAction SilentlyContinue) { duolog " + args + "; exit (Get-Variable LASTEXITCODE -ValueOnly) }; "
	if gate {
		s += "if ([Environment]::GetEnvironmentVariable('DUOLOG_DEPTH') -match '^0*[1-9][0-9]*$') { exit 2 }; "
	}
	return s + "exit 0"
}

// HooksJSON renders the hooks document for one agent, as the plugin ships it.
func HooksJSON(agent, goos string, timeout int) ([]byte, error) {
	hooks := map[string][]group{}
	for _, e := range events(agent, goos, timeout) {
		hooks[e.name] = e.groups
	}
	return encode(map[string]any{"hooks": hooks})
}

// encode indents v without HTML escaping, so commands stay readable.
func encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func isOurs(raw json.RawMessage) bool {
	var g group
	if json.Unmarshal(raw, &g) != nil {
		return false
	}
	for _, h := range g.Hooks {
		if IsOurCommand(h.Command) || IsOurCommand(h.CommandWindows) {
			return true
		}
	}
	return false
}

// mergeHooks returns the file at path with our hook groups replaced by ours
// (or removed when ours is nil). Foreign keys and entries keep their order; our
// groups are replaced in place so a second run gives the same bytes.
func mergeHooks(path string, ours []byte) ([]byte, error) {
	top := map[string]json.RawMessage{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &top); err != nil || top == nil {
			return nil, fmt.Errorf("%s: not a JSON object, nothing changed: %v", path, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	hooks := map[string][]json.RawMessage{}
	if raw, ok := top["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, fmt.Errorf("%s: bad \"hooks\" value, nothing changed: %v", path, err)
		}
	}
	want := map[string][]json.RawMessage{}
	if ours != nil {
		var doc struct {
			Hooks map[string][]json.RawMessage `json:"hooks"`
		}
		if err := json.Unmarshal(ours, &doc); err != nil {
			return nil, err
		}
		want = doc.Hooks
	}
	names := map[string]bool{}
	for name := range hooks {
		names[name] = true
	}
	for name := range want {
		names[name] = true
	}
	for name := range names {
		var merged []json.RawMessage
		pending := want[name]
		for _, raw := range hooks[name] {
			switch {
			case !isOurs(raw):
				merged = append(merged, raw)
			case len(pending) > 0:
				merged = append(merged, pending[0])
				pending = pending[1:]
			}
		}
		merged = append(merged, pending...)
		if len(merged) == 0 {
			delete(hooks, name)
		} else {
			hooks[name] = merged
		}
	}
	if len(hooks) == 0 {
		delete(top, "hooks")
	} else {
		raw, err := encode(hooks)
		if err != nil {
			return nil, err
		}
		top["hooks"] = raw
	}
	return encode(top)
}

// IsOurCommand shares handler recognition with trust and startup checks.
func IsOurCommand(command string) bool {
	return command == "duolog" || command == "duolog gate" || strings.Contains(command, "duolog hook ") || strings.Contains(command, "duolog gate ")
}
