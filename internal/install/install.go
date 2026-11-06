// Package install writes duolog hooks, skills and a starter config for Claude
// Code and Codex, at user or project scope. It only touches paths under
// Options.Home and Options.Root and never the process home directory.
package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/config"
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
	// backup is where the old file goes; empty means next to it.
	backup string
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
	var regated []string // Codex hooks files whose gate command changes
	for _, agent := range agents {
		path := hooksPath(base, agent)
		if o.Scope == "user" {
			path = userHooksPath(base, agent)
		}
		if agent == "claude" && PluginEnabled(base, o.Home) {
			// The plugin already registers these hooks and the skill; a second
			// copy in settings.json would run every gate twice. Drop any copy an
			// earlier install left behind.
			if _, err := os.Stat(path); err == nil {
				data, err := mergeHooks(path, nil)
				if err != nil {
					return Report{}, err
				}
				edits = append(edits, edit{path: path, data: data})
			}
			report.Notes = append(report.Notes, "Claude: the "+pluginKey+" plugin is enabled, so its hooks and skill are used and none were installed here. Gate waits are capped at 120 seconds.")
			continue
		}
		hooks, err := HooksJSON(agent, timeout)
		if err != nil {
			return Report{}, err
		}
		data, err := mergeHooks(path, hooks)
		if err != nil {
			return Report{}, err
		}
		edits = append(edits, edit{path: path, data: data})
		if agent == "codex" {
			// Codex skips a hook whose command changed after approval, and its
			// trusted_hash stays; trust must not count that hash for the new
			// command.
			if old, err := os.ReadFile(path); err == nil && gateCommands(old) != gateCommands(data) {
				regated = append(regated, path)
			}
		}
		for _, name := range plugin.Skills {
			files, err := skillFiles(agent, name)
			if err != nil {
				return Report{}, err
			}
			dir := installedSkillDir(o, base, agent, name)
			for _, rel := range files {
				data, err := plugin.FS.ReadFile(agent + "/skills/" + name + "/" + rel)
				if err != nil {
					return Report{}, err
				}
				edits = append(edits, edit{path: filepath.Join(dir, filepath.FromSlash(rel)), data: data})
			}
		}
	}
	if o.Scope != "user" {
		if _, err := store.Open(o.Root); err != nil {
			return Report{}, err
		}
		if e, ok, err := calibrationIgnoreEdit(o.Root); err != nil {
			return Report{}, err
		} else if ok {
			edits = append(edits, e)
		}
		instructions, err := instructionEdits(o.Root)
		if err != nil {
			return Report{}, err
		}
		edits = append(edits, instructions...)
		path := filepath.Join(o.Root, ".duolog", "config.yaml")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			edits = append(edits, edit{path: path, data: []byte("# yaml-language-server: $schema=" + config.SchemaURL + "\nversion: 1\n")})
		} else if err != nil {
			return Report{}, err
		}
	}
	// A backup next to a project file lands in the work tree, where git add -A
	// picks it up. .duolog/ is ignored, so project backups go there.
	if o.Scope != "user" {
		for i := range edits {
			edits[i].backup = projectBackup(o.Root, edits[i].path)
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
	for _, path := range regated {
		if err := markStaleApprovals(o.Home, path); err != nil {
			return report, err
		}
	}
	for _, agent := range agents {
		if agent == "codex" {
			report.Notes = append(report.Notes, "Codex: approve the duolog hooks in /hooks (press t), and trust this project, or Codex will not run them.")
		}
	}
	return report, nil
}

// calibrationIgnoreEdit appends store.CalibrationIgnore to a .duolog/.gitignore
// written before calibration, so the project part goes to Git. It reports false
// when the file already shares the project part. Existing lines stay.
func calibrationIgnoreEdit(root string) (edit, bool, error) {
	path := filepath.Join(root, ".duolog", ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil {
		return edit{}, false, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "!calibration/project.yaml" {
			return edit{}, false, nil
		}
	}
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		data = append(data, '\n')
	}
	return edit{path: path, data: append(data, store.CalibrationIgnore...)}, true, nil
}

// Remove deletes only what Run added: our hook entries and our skill files.
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
			edits = append(edits, edit{path: path, data: data})
		}
	}
	if o.Scope != "user" {
		for i := range edits {
			edits[i].backup = projectBackup(o.Root, edits[i].path)
		}
	}
	for _, e := range edits {
		if _, err := apply(e); err != nil {
			return err
		}
	}
	for _, agent := range agents {
		for _, name := range plugin.Skills {
			files, err := skillFiles(agent, name)
			if err != nil {
				return err
			}
			dir := installedSkillDir(o, base, agent, name)
			for _, rel := range files {
				path := filepath.Join(dir, filepath.FromSlash(rel))
				// A file with other content is not the one Run wrote, so it stays.
				ours, err := plugin.FS.ReadFile(agent + "/skills/" + name + "/" + rel)
				if err != nil {
					return err
				}
				if have, err := os.ReadFile(path); err != nil || !bytes.Equal(have, ours) {
					continue
				}
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					return err
				}
				// Folders go only when empty, from the file's up to the skill's.
				for d := filepath.Dir(path); d != filepath.Dir(dir); d = filepath.Dir(d) {
					if os.Remove(d) != nil {
						break
					}
				}
			}
		}
	}
	if o.Scope != "user" {
		return removeInstructions(o.Root)
	}
	return nil
}

// skillFiles lists every file of one bundled skill, relative to its folder,
// with slashes.
func skillFiles(agent, name string) ([]string, error) {
	root := agent + "/skills/" + name
	var files []string
	err := fs.WalkDir(plugin.FS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		files = append(files, strings.TrimPrefix(path, root+"/"))
		return nil
	})
	return files, err
}

// installedSkillDir is the folder one bundled skill goes into.
func installedSkillDir(o Options, base, agent, name string) string {
	return filepath.Join(filepath.Dir(filepath.Dir(installedSkillPath(o, base, agent))), name)
}

// pluginKey is the enabledPlugins key of the duolog Claude plugin.
const pluginKey = "duolog@duolog"

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
// copied to <file>.duolog.bak first; an older backup is kept as the original. A
// symlink stays a link: the file it points to is the one that changes. The file
// and its backup keep the existing permissions, because settings can hold keys.
func apply(e edit) (bool, error) {
	path := e.path
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			return false, fmt.Errorf("%s: a link that points nowhere, nothing changed: %w", path, err)
		}
		path = target
	}
	mode := os.FileMode(0o644)
	old, err := os.ReadFile(path)
	if err == nil && bytes.Equal(old, e.data) {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if err == nil {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return false, statErr
		}
		mode = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err == nil {
		backup := e.backup
		if backup == "" {
			backup = path + backupSuffix
		} else if err := os.MkdirAll(filepath.Dir(backup), 0o755); err != nil {
			return false, err
		}
		if err := writeBackup(backup, old, mode); err != nil {
			return false, err
		}
	}
	// A new file in the folder: a fixed name could follow a link planted there.
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(e.data)
	closeErr := tmp.Close()
	if writeErr != nil {
		return false, writeErr
	}
	if closeErr != nil {
		return false, closeErr
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return false, err
	}
	if err := store.Rename(tmp.Name(), path); err != nil {
		return false, err
	}
	return true, nil
}

// writeBackup keeps the first backup, and refuses to follow a link in its place.
func writeBackup(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

type handler struct {
	Type           string   `json:"type"`
	Command        string   `json:"command"`
	CommandWindows string   `json:"commandWindows,omitempty"`
	Args           []string `json:"args,omitempty"`
	Timeout        int      `json:"timeout,omitempty"`
	AsyncRewake    bool     `json:"asyncRewake,omitempty"`
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
func events(agent string, timeout int) []event {
	makeHandler := func(args string, gate bool) handler {
		h := handler{Type: "command", Command: "duolog", Args: strings.Fields(args)}
		if agent == "codex" {
			h.Command = command("linux", args, gate)
			h.Args = nil
			h.CommandWindows = command("windows", args, gate)
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
	list := []event{
		{"SessionStart", start},
		{"SessionEnd", []group{hook("SessionEnd")}},
		{"UserPromptSubmit", []group{hook("UserPromptSubmit")}},
		{"PostToolUse", []group{hook("PostToolUse")}},
		{"PreToolUse", []group{gate("PreToolUse")}},
		{"PermissionRequest", []group{gate("PermissionRequest")}},
	}
	// Only Claude's lead compacts through a hook that adds summary
	// instructions; Codex has none.
	if agent == "claude" {
		list = append(list, event{"PreCompact", []group{hook("PreCompact")}})
	}
	return list
}

// CodexGateCommands returns the POSIX and Windows commands duolog installs for
// the Codex gate on event. Codex runs a hook only while its command is the one
// the person approved, so trust compares an approved gate with these.
func CodexGateCommands(event string) (posix, windows string) {
	args := "gate --agent codex --event " + event
	return command("linux", args, true), command("windows", args, true)
}

// Codex carries both shell guards in one shared file. POSIX must exec so
// registration records the agent, not a short-lived shell.
func command(goos, args string, gate bool) string {
	if goos == "windows" {
		body := powershellGuard(args, gate)
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

// powershellGuard finds duolog with where.exe and reads the exit code without a
// module cmdlet. Measured on hosted Windows runners, x64 and ARM: PowerShell
// starts in 0.2 seconds, but Get-Command for a missing program searches every
// module for 24 seconds, and Get-Variable took 18 to 30 seconds in a worker
// environment without PSModulePath. Every Codex tool call runs this guard.
// [char]36 is the dollar sign, which an outer shell would otherwise expand, and
// $PATH:duolog keeps where.exe out of the current directory.
func powershellGuard(args string, gate bool) string {
	lastExit := "[int][scriptblock]::Create('' + [char]36 + 'LASTEXITCODE').Invoke()[0]"
	s := "& ([Environment]::SystemDirectory + '\\where.exe') /Q ([char]36 + 'PATH:duolog'); if (" + lastExit + " -eq 0) { duolog " + args + "; exit " + lastExit + " }; "
	if gate {
		s += "if ([Environment]::GetEnvironmentVariable('DUOLOG_DEPTH') -match '^0*[1-9][0-9]*$') { exit 2 }; "
	}
	return s + "exit 0"
}

// HooksJSON renders the hooks document for one agent, as the plugin ships it.
func HooksJSON(agent string, timeout int) ([]byte, error) {
	hooks := map[string][]group{}
	for _, e := range events(agent, timeout) {
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

// withoutOurHandlers splits one group of a settings file. It returns the group
// with duolog's handlers removed, and whether it held any of them. rest is nil
// when nothing else is left. A foreign handler keeps its matcher and every field.
func withoutOurHandlers(raw json.RawMessage) (rest json.RawMessage, ours bool, err error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return raw, false, nil //nolint:nilerr // a foreign entry we cannot read is kept as it is
	}
	var handlers []json.RawMessage
	if json.Unmarshal(fields["hooks"], &handlers) != nil {
		return raw, false, nil //nolint:nilerr // a foreign entry we cannot read is kept as it is
	}
	var kept []json.RawMessage
	for _, item := range handlers {
		var h handler
		if json.Unmarshal(item, &h) == nil && (IsOurCommand(h.Command) || IsOurCommand(h.CommandWindows)) {
			ours = true
			continue
		}
		kept = append(kept, item)
	}
	if !ours {
		return raw, false, nil
	}
	if len(kept) == 0 {
		return nil, true, nil
	}
	if fields["hooks"], err = encode(kept); err != nil {
		return nil, false, err
	}
	rest, err = encode(fields)
	return rest, true, err
}

// mergeHooks returns the file at path with our hook groups replaced by ours
// (or removed when ours is nil). Foreign keys and entries keep their order; our
// groups are replaced in place so a second run gives the same bytes.
func mergeHooks(path string, ours []byte) ([]byte, error) {
	top := map[string]json.RawMessage{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &top); err != nil || top == nil {
			return nil, fmt.Errorf("%s: not a JSON object, nothing changed: %v", path, err) //nolint:errorlint // err is nil when top is null, and %w would print %!w(<nil>)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	hooks := map[string][]json.RawMessage{}
	if raw, ok := top["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, fmt.Errorf("%s: bad \"hooks\" value, nothing changed: %w", path, err)
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
			rest, ours, err := withoutOurHandlers(raw)
			if err != nil {
				return nil, err
			}
			if rest != nil {
				merged = append(merged, rest)
			}
			if ours && rest == nil && len(pending) > 0 {
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

// projectBackup names the backup of a file in the project: the same path under
// .duolog/backup/. A file outside the project keeps its backup next to it.
func projectBackup(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return ""
	}
	return filepath.Join(root, ".duolog", "backup", rel)
}
