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
	"strings"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/plugin"
)

// GateTimeout is the timeout, in seconds, that install writes for a gate
// hook: longer than the gate's longest wait.
const GateTimeout = 150

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
// ignores the skills; Run and Remove do not read it.
type Options struct {
	Scope, Agent string
	Home, Root   string
	HooksOnly    bool
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
func (d Deps) Run(o Options) (Report, error) {
	agents, base, err := d.resolve(o)
	if err != nil {
		return Report{}, err
	}
	var edits []edit
	var report Report
	var regated []regate // hooks files whose gate command changes
	for _, a := range agents {
		p, err := planAgent(a, o, base)
		if err != nil {
			return Report{}, err
		}
		if p.hook != nil {
			edits = append(edits, *p.hook)
		}
		report.Notes = append(report.Notes, p.notes...)
		if approvals, ok := a.Installer.(Approvals); ok && !p.plugin {
			// The agent skips a hook whose command changed after approval, and its
			// trusted_hash stays; trust must not count that hash for the new
			// command.
			if p.old != nil && approvals.GateChanged(p.old, p.hook.data) {
				regated = append(regated, regate{approvals, p.hook.path})
			}
		}
		edits = append(edits, p.skills...)
	}
	if o.Scope != "user" {
		project, err := d.projectEdits(o.Root)
		if err != nil {
			return Report{}, err
		}
		edits = append(edits, project...)
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
	for _, r := range regated {
		if err := r.approvals.MarkStale(o.Home, r.path); err != nil {
			return report, err
		}
	}
	// The skills are in the configured folder now; take duolog's own out of the
	// default one. Any other file there stays. Then the lead's window.
	files, notes, err := d.finishInstall(agents, o, base)
	report.Files = append(report.Files, files...)
	report.Notes = append(report.Notes, notes...)
	return report, err
}

// finishInstall removes duolog's skills from the default folder and, in
// project scope, sets the lead's compact window. It returns the files it
// changed and the notes.
func (d Deps) finishInstall(agents []Agent, o Options, base string) ([]string, []string, error) {
	notes, err := dropDefaultSkills(agents, o, base)
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
	note = strings.ToUpper(a.Name[:1]) + a.Name[1:] + ": the " + pluginKey + " plugin is enabled, so its hooks and skill are used and none were installed here. Gate waits are capped at 120 seconds."
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
}

// planAgent plans the writes of agent a for o under base. It writes nothing.
func planAgent(a Agent, o Options, base string) (agentPlan, error) {
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
	p.skills, err = skillEdits(a, o, base)
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

// skillEdits plans the writes of every bundled skill of agent a.
func skillEdits(a Agent, o Options, base string) ([]edit, error) {
	var edits []edit
	for _, name := range plugin.Skills {
		files, err := skillFiles(a.Name, name)
		if err != nil {
			return nil, err
		}
		dir := filepath.Join(a.Installer.SkillDir(o.Scope, base, o.Home), name)
		for _, rel := range files {
			data, err := plugin.FS.ReadFile(a.Name + "/skills/" + name + "/" + rel)
			if err != nil {
				return nil, err
			}
			edits = append(edits, edit{path: filepath.Join(dir, filepath.FromSlash(rel)), data: data})
		}
	}
	return edits, nil
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
func (d Deps) Remove(o Options) error {
	agents, base, err := d.resolve(o)
	if err != nil {
		return err
	}
	var edits []edit
	for _, a := range agents {
		path := a.Installer.HookFile(o.Scope, base, o.Home)
		if old, err := os.ReadFile(path); err == nil {
			data, _, err := a.Installer.Hooks(old, 0)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
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
	for _, a := range agents {
		if err := removeSkills(a, a.Installer.SkillDir(o.Scope, base, o.Home)); err != nil {
			return err
		}
	}
	// An earlier install may have used the default folder.
	if _, err := dropDefaultSkills(agents, o, base); err != nil {
		return err
	}
	if o.Scope != "user" {
		return d.removeProject(agents, o.Root)
	}
	return nil
}

// removeProject removes what a project install added beside the hooks and
// skills: the lead's compact window that install wrote, and the instruction
// blocks.
func (d Deps) removeProject(agents []Agent, root string) error {
	if err := removeLead(agents, root); err != nil {
		return err
	}
	return d.removeInstructions(root)
}

// removeSkills deletes the bundled skill files of agent a from skills, the
// skills folder. A file with other content is not the one Run wrote, so it
// stays. Folders go only when empty, from the file's up to the skill's.
func removeSkills(a Agent, skills string) error {
	for _, name := range plugin.Skills {
		if err := removeSkill(a, name, skills); err != nil {
			return err
		}
	}
	return nil
}

// removeSkill deletes the files of the bundled skill name of agent a from the
// skills folder. Folders go only when empty, from the file's up to the skill's.
func removeSkill(a Agent, name, skills string) error {
	files, err := skillFiles(a.Name, name)
	if err != nil {
		return err
	}
	dir := filepath.Join(skills, name)
	for _, rel := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		ours, err := plugin.FS.ReadFile(a.Name + "/skills/" + name + "/" + rel)
		if err != nil {
			return err
		}
		if have, err := os.ReadFile(path); err != nil || !bytes.Equal(have, ours) {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		for parent := filepath.Dir(path); parent != filepath.Dir(dir); parent = filepath.Dir(parent) {
			if os.Remove(parent) != nil {
				break
			}
		}
	}
	return nil
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

// projectBackup names the backup of a file in the project: the same path under
// .duolog/backup/. A file outside the project keeps its backup next to it.
func projectBackup(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return ""
	}
	return filepath.Join(root, ".duolog", "backup", rel)
}
