package install

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/plugin"
)

// HookParts is implemented by the Installer of an agent whose own hooks Check
// can pick out of a hooks file. OurHooks returns duolog's part of the file
// whose bytes are data (nil for none), by event, in a form that two files
// compare by: the person's hooks and settings are not in it. DevHooks reports
// that duolog's hooks in data run the dev launcher of the duolog checkout at
// root, which install never writes.
type HookParts interface {
	OurHooks(data []byte) (map[string][]string, error)
	DevHooks(data []byte, root string) bool
}

// Status says how one agent's install at one scope compares with what Run
// would write there now.
type Status struct {
	Agent, Scope string
	// Installed is false when the scope has none of duolog's hooks and none of
	// its skill folders for the agent: that is no install, not a stale one.
	Installed bool
	// Dev says that the hooks run the dev launcher of a duolog checkout; Check
	// compares nothing then.
	Dev bool
	// Changed are the files Run would write: missing, or with other content.
	// For the hooks file only duolog's own hooks count.
	Changed []string
	// MissingSkills are duolog's skill folders that are not there at all.
	// Their files are in Changed too.
	MissingSkills []string
	// Extra are files in duolog's skill folders that this duolog does not
	// ship. Run leaves them, so the person removes them; they do not make the
	// install stale.
	Extra []string
	// HookFile is the agent's hooks or settings file at the scope.
	HookFile string
	// PluginHooks says the agent's plugin is enabled and duolog's hooks are in
	// HookFile too, so they run twice. Run would delete them from a file that
	// may be committed and shared, so this is for the person, not a stale
	// install.
	PluginHooks bool
	// SharedDir is the agent's skills folder when it is the same directory as
	// another selected agent's. Each agent then finds the other's duolog skills
	// there, so its skills are not compared, and Run cannot fix it.
	SharedDir string
	// SharesWith names that other agent, on the first of the two only, so the
	// clash is reported once.
	SharesWith string
	// Err is why the install could not be compared.
	Err error
}

// Stale reports whether Run would change the install: a file it writes is
// missing or has other content, or a file it removes is there.
func (s Status) Stale() bool {
	return s.Err == nil && len(s.Changed) > 0
}

// Lines are what doctor and install --check print for the install: the line
// of Line, then each note that needs the person and not install. None means
// the install is current, or not there.
func (s Status) Lines(root string) []string {
	var lines []string
	if line := s.Line(root); line != "" {
		lines = append(lines, line)
	}
	if s.PluginHooks {
		lines = append(lines, s.Agent+": duolog hooks are in "+shown(root, []string{s.HookFile})+" and the duolog plugin is enabled; they run twice")
	}
	if s.SharesWith != "" {
		lines = append(lines, s.Agent+" and "+s.SharesWith+" share "+shown(root, []string{s.SharedDir})+"; duolog writes each agent's own skills there, so keep them apart")
	}
	if len(s.Extra) > 0 {
		lines = append(lines, s.Agent+" "+s.Scope+" install: remove "+shown(root, s.Extra)+", which duolog does not ship")
	}
	return lines
}

// Line is the line that doctor and install --check print for an install that
// is out of date or could not be checked, with the files and the fix, and
// empty for any other. Paths under root are shown relative to it.
func (s Status) Line(root string) string {
	head := s.Agent + " " + s.Scope + " install "
	if s.Err != nil {
		return head + "could not be checked: " + s.Err.Error()
	}
	if !s.Stale() {
		return ""
	}
	var changed []string
	for _, path := range s.Changed {
		if !slices.ContainsFunc(s.MissingSkills, func(dir string) bool { return strings.HasPrefix(path, dir+string(filepath.Separator)) }) {
			changed = append(changed, path)
		}
	}
	var parts []string
	if len(s.MissingSkills) > 0 {
		parts = append(parts, "duolog skills missing: "+shown(root, s.MissingSkills))
	}
	if len(changed) > 0 {
		parts = append(parts, shown(root, changed))
	}
	parts = append(parts, "run duolog install --scope "+s.Scope+" --agent "+s.Agent)
	return head + "is out of date: " + strings.Join(parts, "; ")
}

// shown joins paths, each relative to root when it is under root.
func shown(root string, paths []string) string {
	out := make([]string, len(paths))
	for i, path := range paths {
		out[i] = path
		if rel, err := filepath.Rel(root, path); err == nil && filepath.IsLocal(rel) {
			out[i] = rel
		}
	}
	return strings.Join(out, ", ")
}

// Check compares each selected agent's install at o's scope with what Run
// would write there now: duolog's hooks in the hooks or settings file, and
// duolog's bundled skill folders. It writes nothing. Other hooks, settings
// and skills are the person's and do not count. With o.HooksOnly the skills
// do not count either.
func (d Deps) Check(o Options) ([]Status, error) {
	agents, base, err := d.resolve(o)
	if err != nil {
		return nil, err
	}
	scope := o.Scope
	if scope == "" {
		scope = "project"
	}
	shared, with := sharedSkillDirs(agents, o, base)
	out := make([]Status, 0, len(agents))
	for i, a := range agents {
		s := checkAgent(a, o, base, scope, o.HooksOnly || shared[i] != "")
		s.SharedDir, s.SharesWith = shared[i], with[i]
		checkInstruction(&s, a, o, base)
		out = append(out, s)
	}
	return out, nil
}

// sharedSkillDirs finds the agents whose skills folders are one directory, as
// a link can make them. shared[i] is that folder for agent i, and with[i]
// names the other agent on the first of the two only.
func sharedSkillDirs(agents []Agent, o Options, base string) (shared, with []string) {
	shared, with = make([]string, len(agents)), make([]string, len(agents))
	dirs := make([]string, len(agents))
	for i, a := range agents {
		dirs[i] = a.Installer.SkillDir(o.Scope, base, o.Home)
	}
	for i := range agents {
		for j := i + 1; j < len(agents); j++ {
			if !sameDir(dirs[i], dirs[j]) {
				continue
			}
			shared[i], shared[j] = dirs[i], dirs[i]
			if with[i] == "" {
				with[i] = agents[j].Name
			}
		}
	}
	return shared, with
}

// checkAgent compares agent a's install for o under base. hooksOnly leaves
// its skills out.
func checkAgent(a Agent, o Options, base, scope string, hooksOnly bool) Status {
	s := Status{Agent: a.Name, Scope: scope}
	dir := a.Installer.SkillDir(o.Scope, base, o.Home)
	skills := !hooksOnly && (hasSkills(dir) || hasSkills(defaultSkillDir(a, o, base)))
	p, have, err := readPlan(a, o, base)
	s.HookFile = p.hookFile
	if err != nil {
		// A hooks file that cannot be read may still hold duolog's hooks.
		raw, _ := os.ReadFile(p.hookFile)
		if s.Installed = skills || bytes.Contains(raw, []byte("duolog")); s.Installed {
			s.Err = err
		}
		return s
	}
	if s.Installed = len(have) > 0 || skills; !s.Installed {
		return s
	}
	parts, _ := a.Installer.(HookParts)
	if scope == "project" && parts.DevHooks(p.old, base) {
		s.Dev = true
		return s
	}
	s.PluginHooks = p.plugin && len(have) > 0
	if s.Changed, s.Err = changedFiles(parts, p, have, hooksOnly); s.Err == nil && !hooksOnly {
		var leftover []string
		s.MissingSkills, s.Extra, leftover, s.Err = skillState(a, o, base, dir, p.plugin)
		s.Changed = append(s.Changed, leftover...)
	}
	return s
}

// checkInstruction adds agent a's project instruction file to s.Changed when
// it does not hold the current duolog block: missing, outdated or without
// one. It leaves an install that is not there, runs the dev launcher or could
// not be read, and a check of hooks alone.
func checkInstruction(s *Status, a Agent, o Options, root string) {
	if !s.Installed || s.Dev || s.Err != nil || s.Scope != "project" || o.HooksOnly {
		return
	}
	path, _ := a.Installer.InstructionFiles(root)
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		s.Err = err
		return
	}
	current, _, err := agent.BlockCurrent(data)
	switch {
	case err != nil:
		s.Err = fmt.Errorf("%s: %w", path, err)
	case !current:
		s.Changed = append(s.Changed, path)
	}
}

// hasSkills reports whether any of duolog's bundled skill folders is in the
// skills folder dir; an empty dir has none.
func hasSkills(dir string) bool {
	return dir != "" && slices.ContainsFunc(plugin.Skills, func(name string) bool { return isDir(filepath.Join(dir, name)) })
}

// skillState compares duolog's skill folders of agent a in dir, the folder
// the scope selects, as skillFolders does, unless the agent's plugin supplies
// the skills. leftover are the files of duolog's skill folders in the default
// folder, when a config key moved the skills to another one: Run removes
// them whatever their content. A skill there behind a link is not one: Run
// leaves it, so it is no difference.
func skillState(a Agent, o Options, base, dir string, pluginOn bool) (missing, extra, leftover []string, err error) {
	if !pluginOn {
		if missing, extra, err = skillFolders(a.Name, dir); err != nil {
			return nil, nil, nil, err
		}
	}
	leftover, err = defaultSkillFiles(defaultSkillDir(a, o, base), base)
	return missing, extra, leftover, err
}

// readPlan plans agent a's install and reads duolog's hooks in its hooks
// file now. The plan names the hooks file even on an error.
func readPlan(a Agent, o Options, base string) (agentPlan, map[string][]string, error) {
	parts, ok := a.Installer.(HookParts)
	if !ok {
		return agentPlan{hookFile: a.Installer.HookFile(o.Scope, base, o.Home)}, nil, errors.New("its hooks cannot be compared")
	}
	p, err := planAgent(a, o, base)
	if err != nil {
		return p, nil, err
	}
	have, err := parts.OurHooks(p.old)
	if err != nil {
		return p, nil, fmt.Errorf("%s: %w", p.hookFile, err)
	}
	return p, have, nil
}

// changedFiles lists the files of plan p that differ from what is there: the
// hooks file when duolog's hooks in it, have, are not the planned ones, and
// unless hooksOnly, each skill file that is missing or has other text. With
// the plugin enabled the hooks file does not count: Status.PluginHooks says
// what is wrong there.
func changedFiles(parts HookParts, p agentPlan, have map[string][]string, hooksOnly bool) ([]string, error) {
	var changed []string
	if p.hook != nil && !p.plugin {
		want, err := parts.OurHooks(p.hook.data)
		if err != nil {
			return nil, err
		}
		if !maps.EqualFunc(have, want, slices.Equal) {
			changed = append(changed, p.hookFile)
		}
	}
	if hooksOnly {
		return changed, nil
	}
	for _, e := range p.skills {
		data, err := os.ReadFile(e.path)
		if err != nil || !sameText(data, e.data) {
			changed = append(changed, e.path)
		}
	}
	return changed, nil
}

// skillFolders names the bundled skill folders of agent that are missing
// under dir, and the files in the others that the bundle does not have. An
// install backup and the files a file manager adds do not count.
func skillFolders(agent, dir string) (missing, extra []string, err error) {
	for _, name := range plugin.Skills {
		folder := filepath.Join(dir, name)
		if !isDir(folder) {
			missing = append(missing, folder)
			continue
		}
		files, err := skillFiles(agent, name)
		if err != nil {
			return nil, nil, err
		}
		err = filepath.WalkDir(folder, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || path == folder || ignoredInSkill(d.Name()) {
				return nil //nolint:nilerr // a folder that cannot be read is compared by its files
			}
			if rel, err := filepath.Rel(folder, path); err == nil && !slices.Contains(files, filepath.ToSlash(rel)) {
				extra = append(extra, path)
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	return missing, extra, nil
}

// ignoredInSkill reports whether a file of this name in a skill folder is
// none of duolog's business: a backup that install wrote at user scope, a
// temporary file of a write in progress, what a file manager adds, or what an
// editor leaves beside a file it edits (name~, .name.swp, #name#).
func ignoredInSkill(name string) bool {
	switch name {
	case ".DS_Store", "Thumbs.db", "desktop.ini":
		return true
	}
	hidden := strings.HasPrefix(name, ".")
	return strings.HasSuffix(name, backupSuffix) || strings.HasSuffix(name, "~") ||
		hidden && (strings.HasSuffix(name, ".tmp") || strings.HasSuffix(name, ".swp")) ||
		len(name) > 1 && strings.HasPrefix(name, "#") && strings.HasSuffix(name, "#")
}

// sameText compares two texts with LF and CRLF line endings taken as equal:
// Git on Windows can check out a committed skill with CRLF.
func sameText(a, b []byte) bool {
	crlf, lf := []byte("\r\n"), []byte("\n")
	return bytes.Equal(bytes.ReplaceAll(a, crlf, lf), bytes.ReplaceAll(b, crlf, lf))
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// StaleLines checks the project install at root and the user install under
// home, every agent, and returns the lines of each install that is out of
// date, could not be checked or needs the person (see Status.Lines). An
// empty home skips the user scope.
func (d Deps) StaleLines(root, home string) []string {
	var lines []string
	for _, scope := range []string{"project", "user"} {
		if scope == "user" && home == "" {
			continue
		}
		statuses, err := d.Check(Options{Scope: scope, Root: root, Home: home})
		if err != nil {
			lines = append(lines, scope+" install could not be checked: "+err.Error())
			continue
		}
		for _, s := range statuses {
			lines = append(lines, s.Lines(root)...)
		}
	}
	return lines
}

// ProjectNote is the note a lead's session start adds when the project
// install at root is out of date, so that install would change it. It is
// empty when the install is current or missing, for what install would not
// fix (extra files, hooks beside the plugin, a shared skills folder), and
// when any check fails: the hook must never fail on it.
func (d Deps) ProjectNote(root, home string) string {
	statuses, err := d.Check(Options{Scope: "project", Root: root, Home: home})
	if err != nil {
		return ""
	}
	stale, missing := false, false
	for _, s := range statuses {
		if s.Err != nil {
			return ""
		}
		if s.Stale() {
			stale = true
			missing = missing || len(s.MissingSkills) > 0
		}
	}
	switch {
	case missing:
		return "duolog: this project's duolog skills are missing; run duolog install --scope project"
	case stale:
		return "duolog: this project's duolog install is out of date; run duolog install --scope project"
	}
	return ""
}
