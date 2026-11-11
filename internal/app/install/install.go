// Package install writes duolog hooks, skills and a starter config for each
// registered agent, at user or project scope. It only touches paths under
// Options.Home and Options.Root and never the process home directory. What
// differs per agent, the hooks and where they and the skill go, is the
// agent's Installer.
package install

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/plugin"
)

// GateTimeout is the timeout, in seconds, that install writes for a gate
// hook: longer than the gate's longest wait, the ten minutes of an ask only
// the person can answer.
const GateTimeout = 630

// Installer is what Run and Remove ask of one agent.
//
// HookFile is the settings or hooks file that scope selects, under base (the
// project root, or the home for user scope) or home. Hooks merges the agent's
// hooks, with gate timeouts of timeout seconds, into the file whose bytes are
// existing (nil for a missing file), and returns the new bytes and the steps
// the person still has to do; a timeout of zero removes duolog's hooks
// instead. SkillDir is the folder the bundled skills go into.
// InstructionFiles names the project's instruction file for the agent and
// every place one could have been written.
type Installer interface {
	HookFile(scope, base, home string) string
	Hooks(existing []byte, timeout int) ([]byte, []string, error)
	SkillDir(scope, base, home string) string
	InstructionFiles(root string) (current string, all []string)
}

// SkillDefaults is implemented by the Installer of an agent whose project
// skills folder a config key can move. DefaultSkillDir is the default folder
// when SkillDir names another one, and "" otherwise. Run moves duolog's own
// skills out of it, and Remove removes them from it too.
type SkillDefaults interface {
	DefaultSkillDir(scope, base, home string) string
}

// Plugins is implemented by the Installer of an agent that has a plugin
// registering the same hooks and skill: Run installs none of them where the
// plugin is enabled for root, and drops a copy an earlier install left.
type Plugins interface {
	Enabled(root, home string, environ []string) (bool, error)
}

// Approvals is implemented by the Installer of an agent that approves each
// hook by its command: GateChanged says the gate command differs between two
// bytes of a hooks file, and MarkStale records the approvals that the change
// made stale.
type Approvals interface {
	GateChanged(old, updated []byte) bool
	MarkStale(home, path string) error
}

// Agent holds what install asks of one registered agent.
type Agent struct {
	Installer Installer
	// Name is the agent's name, which the bundled skills are filed under.
	Name string
}

// Deps is what install reaches outside the package: the registered agents in
// order. The wiring package fills it with the real adapters.
//
// LeadWindow reads the compact window of the lead in the project at root: the
// workers.compact_window rule for the Claude plan the quota read names, with
// the bounded fallback when it names none. A project install writes it for
// each agent whose Installer is a LeadEnv; nil leaves the lead alone, and
// Check compares none. Environ is the environment the person's own value is
// looked up in; nil means duolog's own.
type Deps struct {
	Agents     []Agent
	LeadWindow func(root string) (LeadWindow, error)
	Environ    []string
}

// Options selects what to install and where. Scope is "project" (default) or
// "user"; Agent is an agent's name or "both" (default). HooksOnly makes Check
// compare duolog's hooks alone, for a repository that commits the hooks and
// ignores the skills; Run and Remove do not read it. StatusLine makes Run add
// duolog's status line again after the person removed it.
type Options struct {
	Scope, Agent string
	Home, Root   string
	HooksOnly    bool
	StatusLine   bool
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
	// state marks an agent's own state file, which is written in place with
	// no backup (see writeState).
	state bool
	// source is the state snapshot a planned MCP edit must still match.
	source *[]byte
	// plain is a file duolog wrote, such as a skill file at user scope: it
	// is replaced with no backup.
	plain bool
	// restart marks the MCP server entry and the user skills: a session that
	// is already running does not load them until it restarts.
	restart bool
	// writer writes a server file its agent's own way (ServerWriter); nil
	// means as state says.
	writer func(path string, old, data []byte) (bool, error)
}

// RestartNote is the line install prints when it added or changed the MCP
// server entry or the user skills, and the line a lead's hook prints when
// they are newer than its session.
const RestartNote = "Restart running Claude Code and Codex sessions to load the duolog MCP server and skills."

// wroteEdit reports whether a file in report.Files was an edit that which
// picks.
func wroteEdit(edits []edit, report Report, which func(edit) bool) bool {
	written := map[string]bool{}
	for _, f := range report.Files {
		written[f] = true
	}
	return slices.ContainsFunc(edits, func(e edit) bool { return which(e) && written[e.path] })
}

// restartNeeded reports whether a written file in report.Files was an edit
// marked restart.
func restartNeeded(edits []edit, report Report) bool {
	return wroteEdit(edits, report, func(e edit) bool { return e.restart })
}

// stampServer records that install wrote the MCP server entry, for the check
// of a lead started earlier (Deps.NewerThan). The time is the content too, so
// that the file changes on every write. It cannot fail an install that is
// already written: without the stamp that lead is only not told.
func stampServer(home string) {
	if home == "" {
		return
	}
	path := serverStamp(home)
	if os.MkdirAll(filepath.Dir(path), 0o700) == nil {
		_ = os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o600)
	}
}

// Run installs hooks, the skill and, in project scope, a starter config.
func (d Deps) Run(o Options) (Report, error) {
	agents, base, err := d.resolveForInstall(o)
	if err != nil {
		return Report{}, err
	}
	edits, regated, notes, err := planAgents(agents, o, base)
	if err != nil {
		return Report{}, err
	}
	report := Report{Notes: notes}
	if o.Scope != "user" {
		project, err := d.projectEdits(o.Root)
		if err != nil {
			return Report{}, err
		}
		edits = append(edits, project...)
		// A backup next to a project file lands in the work tree, where git add
		// -A picks it up. .duolog/ is ignored, so project backups go there.
		for i := range edits {
			edits[i].backup = projectBackup(o.Root, edits[i].path)
		}
	}
	if err := writeEdits(edits, &report); err != nil {
		return report, err
	}
	for _, r := range regated {
		if err := r.approvals.MarkStale(o.Home, r.path); err != nil {
			return report, err
		}
	}
	restart := restartNeeded(edits, report)
	if wroteEdit(edits, report, func(e edit) bool { return e.restart && e.state }) {
		stampServer(o.Home)
	}
	// The status line follows the hooks, so that a user settings file is read
	// as they left it.
	statusFiles, statusNotes, err := d.installStatusLine(agents, o)
	report.Files = append(report.Files, statusFiles...)
	report.Notes = append(report.Notes, statusNotes...)
	if err != nil {
		return report, err
	}
	if o.Scope == "user" {
		if restart {
			report.Notes = append(report.Notes, RestartNote)
		}
		return report, nil
	}
	// The skills are at user scope now; take the unchanged copies an earlier
	// install wrote out of the project. Any other file there stays. Then the
	// lead's window.
	files, notes, err := d.finishInstall(agents, o)
	report.Files = append(report.Files, files...)
	report.Notes = append(report.Notes, notes...)
	if restart {
		report.Notes = append(report.Notes, RestartNote)
	}
	return report, err
}

// planAgents plans the writes of every agent in agents for o under base,
// with the hooks files whose gate command changes and the notes to print.
func planAgents(agents []Agent, o Options, base string) ([]edit, []regate, []string, error) {
	var edits []edit
	var notes []string
	var regated []regate
	for _, a := range agents {
		p, err := planAgent(a, o, base)
		if err != nil {
			return nil, nil, nil, err
		}
		if p.hook != nil {
			edits = append(edits, *p.hook)
		}
		notes = append(notes, hookChangeNotes(a, p)...)
		if approvals, ok := a.Installer.(Approvals); ok && !p.plugin {
			// The agent skips a hook whose command changed after approval, and its
			// trusted_hash stays; trust must not count that hash for the new
			// command.
			if p.old != nil && approvals.GateChanged(p.old, p.hook.data) {
				regated = append(regated, regate{approvals, p.hook.path})
			}
		}
		edits = append(edits, p.skills...)
		edits = append(edits, p.mcp...)
	}
	return edits, regated, notes, nil
}

// writeEdits writes each edit and adds the path of each file that changed to
// report.
func writeEdits(edits []edit, report *Report) error {
	for _, e := range edits {
		changed, err := e.write()
		if err != nil {
			return err
		}
		if changed {
			report.Files = append(report.Files, e.path)
		}
	}
	return nil
}

// finishInstall ends a project install: it removes the unchanged copies of
// duolog's skills that an earlier install wrote into the project, and sets
// the lead's compact window. It returns the files it changed and the notes.
func (d Deps) finishInstall(agents []Agent, o Options) ([]string, []string, error) {
	notes, err := dropProjectCopies(agents, o)
	if err != nil {
		return nil, notes, err
	}
	files, leadNotes, err := d.installLead(agents, o)
	return files, append(notes, leadNotes...), err
}

// pluginEdits reports whether the plugin of agent a is enabled for base. The
// plugin already registers the hooks and the skill, and a second copy in
// settings.json would run every gate twice, so the edits drop any copy an
// earlier install left behind, and the note tells the person.
func pluginEdits(a Agent, o Options, base, path string) (edits []edit, note string, enabled bool, err error) {
	plugins, ok := a.Installer.(Plugins)
	if !ok {
		return nil, "", false, nil
	}
	enabled, err = plugins.Enabled(base, o.Home, os.Environ())
	if err != nil || !enabled {
		return nil, "", false, err
	}
	if old, err := os.ReadFile(path); err == nil {
		data, _, err := a.Installer.Hooks(old, 0)
		if err != nil {
			return nil, "", false, fmt.Errorf("%s: %w", path, err)
		}
		edits = append(edits, edit{path: path, data: data})
	}
	note = strings.ToUpper(a.Name[:1]) + a.Name[1:] + ": the " + pluginKey + " plugin is enabled, so its hooks and skill are used and none were installed here. Gate waits fit the plugin's hook timeouts."
	return edits, note, true, nil
}

// projectEdits plans what a project install adds beside the hooks and skills:
// the calibration ignore line, the instruction files and a starter config.
func (d Deps) projectEdits(root string) ([]edit, error) {
	if _, err := store.Open(root); err != nil {
		return nil, err
	}
	var edits []edit
	if e, ok, err := calibrationIgnoreEdit(root); err != nil {
		return nil, err
	} else if ok {
		edits = append(edits, e)
	}
	instructions, err := d.instructionEdits(root)
	if err != nil {
		return nil, err
	}
	edits = append(edits, instructions...)
	path := filepath.Join(root, ".duolog", "config.yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		edits = append(edits, edit{path: path, data: []byte("# yaml-language-server: $schema=" + config.SchemaURL + "\nversion: 1\n")})
	} else if err != nil {
		return nil, err
	}
	return edits, nil
}

// agentPlan is what Run writes for one agent, and what Check compares with
// the files: the hooks or settings file and the bundled skills.
type agentPlan struct {
	// hookFile is the hooks or settings file of the scope, old its bytes now
	// (nil when there is none), and hook its new bytes; hook is nil when Run
	// leaves the file alone.
	hookFile string
	old      []byte
	hook     *edit
	// plugin says the agent's plugin is enabled and supplies the hooks and
	// skills: Run then only drops a copy an earlier install left, and writes
	// no skill.
	plugin bool
	skills []edit
	notes  []string
	// mcp is the write of the agent's MCP server entry, for an agent that
	// has one (see MCPServers); its allow entries are in hook.
	mcp []edit
}

// planAgent plans the writes of agent a for o under base: its hooks and
// skills, then its MCP server and allow entries. It writes nothing.
func planAgent(a Agent, o Options, base string) (agentPlan, error) {
	p, err := planHooks(a, o, base)
	if err != nil {
		return p, err
	}
	err = planMCP(&p, a, o, base)
	return p, err
}

// planHooks plans the hooks and skills of agent a for o under base.
func planHooks(a Agent, o Options, base string) (agentPlan, error) {
	p := agentPlan{hookFile: a.Installer.HookFile(o.Scope, base, o.Home)}
	dropped, note, enabled, err := pluginEdits(a, o, base, p.hookFile)
	if err != nil {
		return p, err
	}
	if enabled {
		p.plugin, p.notes = true, []string{note}
		if len(dropped) > 0 {
			p.hook = &dropped[0]
			p.old, _ = os.ReadFile(p.hookFile)
		}
		return p, nil
	}
	old, err := readIfExists(p.hookFile)
	if err != nil {
		return p, err
	}
	data, notes, err := a.Installer.Hooks(old, GateTimeout)
	if err != nil {
		return p, fmt.Errorf("%s: %w", p.hookFile, err)
	}
	p.old, p.hook, p.notes = old, &edit{path: p.hookFile, data: data}, notes
	skills, more, err := userSkillEdits(a, o.Home)
	p.skills, p.notes = skills, append(p.notes, more...)
	return p, err
}

// regate is a hooks file whose gate command changed, with the agent's way to
// mark the approvals of the old command stale.
type regate struct {
	approvals Approvals
	path      string
}

// readIfExists returns the bytes of the file at path, nil when there is none.
func readIfExists(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err == nil && data == nil {
		data = []byte{}
	}
	return data, err
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

// Remove deletes only what Run added: our hook entries, allow entries and
// skill files, and the MCP server entry (see Uninstall). Foreign hooks, the
// config file and everything else stay.
func (d Deps) Remove(o Options) error {
	_, err := d.Uninstall(o)
	return err
}

// Uninstall is Remove with its report: the files it changed, and notes. The
// MCP server entry and the skills are at the agent's user scope, so an
// uninstall at either scope removes them for every project and says so; an
// entry under duolog's name that runs something else stays, with a note, and
// so does a skill folder duolog did not write.
func (d Deps) Uninstall(o Options) (Report, error) {
	agents, base, err := d.resolve(o)
	if err != nil {
		return Report{}, err
	}
	edits, notes, err := removalEdits(agents, o, base)
	if err != nil {
		return Report{}, err
	}
	report := Report{Notes: notes}
	if o.Scope != "user" {
		for i := range edits {
			edits[i].backup = projectBackup(o.Root, edits[i].path)
		}
	}
	if err := writeEdits(edits, &report); err != nil {
		return report, err
	}
	if err := dropAllowRecords(agents, o, base); err != nil {
		return report, err
	}
	// The skills are at user scope for every project: they go, with a note,
	// as the MCP server entry does.
	removed := false
	for _, a := range agents {
		gone, kept, err := removeUserSkills(a, o.Home)
		report.Notes = append(report.Notes, kept...)
		if err != nil {
			return report, err
		}
		removed = removed || gone
	}
	if removed {
		report.Notes = append(report.Notes, SkillsRemovedNote)
	}
	if o.Scope == "user" {
		files, err := d.removeStatusLine(agents, o, false)
		report.Files = append(report.Files, files...)
		return report, err
	}
	// An earlier install may have put copies into the project.
	notes, err = dropProjectCopies(agents, o)
	report.Notes = append(report.Notes, notes...)
	if err != nil {
		return report, err
	}
	return report, d.removeProject(agents, o)
}

// removeProject removes what a project install added beside the hooks, the
// skills and the MCP server: the lead's compact window that install wrote,
// and the instruction blocks.
func (d Deps) removeProject(agents []Agent, o Options) error {
	root := o.Root
	// An older install recorded in the lead marker that it created the local
	// settings file, before the status line had a marker of its own.
	leadCreated := false
	if m := readMarker(root); m != nil {
		leadCreated = m.Created
	}
	if err := removeLead(agents, root); err != nil {
		return err
	}
	// After the lead: the file the status line created goes once both are out.
	if _, err := d.removeStatusLine(agents, o, leadCreated); err != nil {
		return err
	}
	return d.removeInstructions(root)
}

// removalEdits plans what Uninstall writes for agents: each hooks or settings
// file without duolog's hooks and allow entries, and each state file without
// duolog's MCP server, with the notes to print.
func removalEdits(agents []Agent, o Options, base string) ([]edit, []string, error) {
	var edits []edit
	var notes []string
	for _, a := range agents {
		path := a.Installer.HookFile(o.Scope, base, o.Home)
		if old, err := os.ReadFile(path); err == nil {
			data, _, err := a.Installer.Hooks(old, 0)
			if err == nil {
				data, err = dropAllow(a, data, allowRecord(a, o, base))
			}
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %w", path, err)
			}
			edits = append(edits, edit{path: path, data: data})
		}
		server, note, err := serverRemoval(a, o)
		if err != nil {
			return nil, nil, err
		}
		edits, notes = append(edits, server...), append(notes, note...)
	}
	return edits, notes, nil
}

// pruneUp removes dir when it is empty, then the folders above it that are
// empty, up to but not including base.
func pruneUp(dir, base string) {
	for dir != base && strings.HasPrefix(dir, base+string(filepath.Separator)) {
		if info, err := os.Lstat(dir); err != nil || !info.IsDir() || isLinkMode(info.Mode()) {
			return
		}
		if os.Remove(dir) != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
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

// pluginKey is the enabledPlugins key of the duolog Claude plugin.
const pluginKey = "duolog@duolog"

func (d Deps) resolve(o Options) (agents []Agent, base string, err error) {
	names := make([]string, len(d.Agents))
	for i, a := range d.Agents {
		names[i] = a.Name
	}
	switch o.Agent {
	case "", "both":
		agents = d.Agents
	default:
		for _, a := range d.Agents {
			if a.Name == o.Agent {
				agents = []Agent{a}
			}
		}
		if agents == nil {
			return nil, "", fmt.Errorf("unknown agent %q: use %s or both", o.Agent, strings.Join(names, ", "))
		}
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
	if err == nil && !e.plain {
		if err := keepBackup(e, path, old, mode); err != nil {
			return false, err
		}
	}
	return true, replaceFile(path, e.data, mode)
}

// keepBackup saves old, the bytes of the file at path that e replaces, at
// e.backup, or next to the file when that is empty.
func keepBackup(e edit, path string, old []byte, mode os.FileMode) error {
	backup := e.backup
	if backup == "" {
		backup = path + backupSuffix
	} else if err := os.MkdirAll(filepath.Dir(backup), 0o755); err != nil {
		return err
	}
	return writeBackup(backup, old, mode)
}

// replaceFile writes data to path with mode, through a new file in the same
// folder: a fixed name could follow a link planted there.
func replaceFile(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return store.Rename(tmp.Name(), path)
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

// projectBackup names the backup of a file in the project: the same path under
// .duolog/backup/. A file outside the project keeps its backup next to it.
func projectBackup(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return ""
	}
	return filepath.Join(root, ".duolog", "backup", rel)
}
