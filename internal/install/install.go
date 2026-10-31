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

const (
	backupSuffix = ".duolog.bak"
	// graceSeconds is added to policy.ask_timeout_seconds for gate hooks, so the
	// host never kills a hook that is still waiting for the lead's answer.
	graceSeconds = 30
)

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
	timeout := 0
	if cfg, err := config.Load(o.Root); err != nil {
		return Report{}, err
	} else {
		timeout = cfg.Policy.AskTimeoutSeconds + graceSeconds
	}
	var edits []edit
	var report Report
	for _, agent := range agents {
		hooks, err := HooksJSON(agent, runtime.GOOS, timeout)
		if err != nil {
			return Report{}, err
		}
		path := hooksPath(base, agent)
		data, err := mergeHooks(path, hooks)
		if err != nil {
			return Report{}, err
		}
		skill, err := plugin.FS.ReadFile(agent + "/skills/duolog/SKILL.md")
		if err != nil {
			return Report{}, err
		}
		edits = append(edits, edit{path, data}, edit{skillPath(base, agent), skill})
	}
	if o.Scope != "user" {
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
		skill := skillPath(base, agent)
		if err := os.Remove(skill); err != nil && !os.IsNotExist(err) {
			return err
		}
		_ = os.Remove(filepath.Dir(skill)) // only succeeds when empty
	}
	return nil
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
	if err := os.Rename(tmp, e.path); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	return true, nil
}

type handler struct {
	Type        string `json:"type"`
	Command     string `json:"command"`
	Timeout     int    `json:"timeout,omitempty"`
	AsyncRewake bool   `json:"asyncRewake,omitempty"`
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
	hook := func(name string) group {
		return group{[]handler{{Type: "command", Command: command(goos, "hook "+name+" --agent "+agent, false)}}}
	}
	gate := func(name string) group {
		return group{[]handler{{Type: "command", Command: command(goos, "gate --agent "+agent+" --event "+name, true), Timeout: timeout}}}
	}
	start := []group{hook("SessionStart")}
	if agent == "claude" {
		wake := group{[]handler{{Type: "command", Command: command(goos, "hook wake --agent claude", false), AsyncRewake: true}}}
		start = append(start, wake)
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

// command wraps a duolog call in a small per-OS guard. A missing binary must
// not block the lead, so the guard exits 0. A gate fails closed for a worker:
// with DUOLOG_DEPTH of 1 or more and no binary, it exits 2 to block the call.
func command(goos, args string, gate bool) string {
	if goos == "windows" {
		s := "where duolog >nul 2>nul && duolog " + args
		if gate {
			s += ` || if defined DUOLOG_DEPTH if not "%DUOLOG_DEPTH%"=="0" exit /b 2`
		}
		return s
	}
	s := "if command -v duolog >/dev/null 2>&1; then duolog " + args + "; "
	if gate {
		s += `elif [ "${DUOLOG_DEPTH:-0}" -ge 1 ] 2>/dev/null; then exit 2; `
	}
	return s + "fi"
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
		if strings.Contains(h.Command, "duolog hook ") || strings.Contains(h.Command, "duolog gate ") {
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
