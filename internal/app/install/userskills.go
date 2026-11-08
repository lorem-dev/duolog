package install

// This file holds duolog's skills at user scope. Every install writes them
// there, for every project, and each SKILL.md names the duolog version that
// wrote it. The copies that earlier installs wrote into a project go when
// they are unchanged.

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // Git's object id, not a security check
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lorem-dev/duolog/plugin"
)

// versionKey is the key under metadata in a skill's frontmatter that names
// the duolog version that wrote it.
const versionKey = "duolog-version"

// SkillsRemovedNote is what uninstall says when it removes duolog's user
// skills: they are at user scope, so every project loses them.
const SkillsRemovedNote = "duolog skills removed for all projects; other duolog projects get them back with duolog install"

// SkillVersion returns the duolog version that the frontmatter of a SKILL.md
// names under metadata, and "" when it names none: duolog did not write it,
// or an older duolog did.
func SkillVersion(data []byte) string {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return ""
	}
	front, _, ok := strings.Cut(rest, "\n---")
	if !ok {
		return ""
	}
	meta := false
	for _, line := range strings.Split(front, "\n") {
		trimmed := strings.TrimSpace(line)
		if line == trimmed {
			meta = line == "metadata:"
			continue
		}
		if v, ok := strings.CutPrefix(trimmed, versionKey+":"); ok && meta {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

// Version is the duolog version that this binary's bundled skills name.
// A test in cmd/duolog keeps it equal to the binary's version.
func Version() string {
	data, err := plugin.FS.ReadFile("claude/skills/duolog/SKILL.md")
	if err != nil {
		return ""
	}
	return SkillVersion(data)
}

// olderVersion reports whether version a is older than b, each as x.y.z with
// an optional v. A pre-release counts as its release, and a version that does
// not parse is older than none.
func olderVersion(a, b string) bool {
	x, okA := versionParts(a)
	y, okB := versionParts(b)
	if !okA || !okB {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return false
}

func versionParts(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// blobID is Git's object id of a file whose bytes are data, with CRLF taken
// as LF: Git on Windows can check out or copy a skill with CRLF.
func blobID(data []byte) string {
	data = []byte(strings.ReplaceAll(string(data), "\r\n", "\n"))
	h := sha1.New() //nolint:gosec // Git's object id, not a security check
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// versionPlaceholder stands for the version a SKILL.md names in contentID.
const versionPlaceholder = "{duolog-version}"

// contentID is the id of a skill file by its text: Git's object id of data,
// with the version that its frontmatter names, wherever it appears, as
// versionPlaceholder. A release changes only that version in a skill, so
// one id holds for every version with the same text. A file that names no
// version keeps its Git object id.
func contentID(data []byte) string {
	if v := SkillVersion(data); v != "" {
		data = bytes.ReplaceAll(data, []byte(v), []byte(versionPlaceholder))
	}
	return blobID(data)
}

// bundledBlobs are the content ids of every file this binary's bundled
// skills ship, for both agents.
var bundledBlobs = sync.OnceValue(func() map[string]bool {
	out := map[string]bool{}
	_ = fs.WalkDir(plugin.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.Contains(path, "/skills/") {
			return err
		}
		if data, err := plugin.FS.ReadFile(path); err == nil {
			out[contentID(data)] = true
		}
		return nil
	})
	return out
})

// duologWrote reports whether data is a file of a bundled skill as some
// duolog wrote it: this one, or any other version (writtenBlobs).
func duologWrote(data []byte) bool {
	id := contentID(data)
	return writtenBlobs[id] || bundledBlobs()[id]
}

// userSkillDir is the folder of agent a's skills at user scope under home,
// and "" without a home: the skills are never written under the working
// directory.
func userSkillDir(a Agent, home string) string {
	if home == "" {
		return ""
	}
	return a.Installer.SkillDir("user", home, home)
}

// skillState is what a skill folder at user scope holds.
type skillState int

const (
	skillMissing skillState = iota // no folder
	skillCurrent                   // duolog's, with this binary's files
	skillOld                       // duolog's, each file as some duolog wrote it, not all as this one does
	skillEdited                    // duolog's, then changed by the person: install leaves it
	skillForeign                   // not duolog's, or with a link: install leaves it
)

// readUserSkill tells what the folder dir of the bundled skill name of agent
// holds, with the version its SKILL.md names. A folder whose files are each
// one some duolog wrote is duolog's, current or old, and install may replace
// it. One whose SKILL.md names a version but that holds other files too is
// one the person edited, and stays; duolog marked its skills only from 1.6.0
// on, so one that names none is not duolog's. A folder that is a link, or
// holds one or another file that is not regular at any depth, is not
// duolog's either: a write would go through it.
func readUserSkill(agent, name, dir string) (skillState, string, error) {
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return skillMissing, "", nil
	}
	if err != nil {
		return 0, "", err
	}
	if isLinkMode(info.Mode()) || !info.IsDir() {
		return skillForeign, "", nil
	}
	if odd, err := hasOddEntry(dir); err != nil || odd {
		return skillForeign, "", err
	}
	version := ""
	if data, err := os.ReadFile(filepath.Join(dir, "SKILL.md")); err == nil {
		version = SkillVersion(data)
	}
	ours, err := onlyDuologFiles(dir)
	switch {
	case err != nil:
		return 0, "", err
	case !ours && version == "":
		return skillForeign, "", nil
	case !ours:
		return skillEdited, version, nil
	}
	changed, err := changedSkillFiles(agent, name, dir)
	if err != nil {
		return 0, "", err
	}
	if len(changed) > 0 {
		return skillOld, version, nil
	}
	return skillCurrent, version, nil
}

// changedSkillFiles lists the files of the bundled skill name of agent that
// are missing from dir or have other text.
func changedSkillFiles(agent, name, dir string) ([]string, error) {
	files, err := skillFiles(agent, name)
	if err != nil {
		return nil, err
	}
	var changed []string
	for _, rel := range files {
		ours, err := plugin.FS.ReadFile(agent + "/skills/" + name + "/" + rel)
		if err != nil {
			return nil, err
		}
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if have, err := os.ReadFile(path); err != nil || !sameText(have, ours) {
			changed = append(changed, path)
		}
	}
	return changed, nil
}

// hasOddEntry reports whether anything in dir, at any depth, is a link or a
// file that is neither regular nor a folder.
func hasOddEntry(dir string) (bool, error) {
	odd := false
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case path == dir || d.IsDir() || d.Type().IsRegular():
			return nil
		}
		odd = true
		return filepath.SkipAll
	})
	return odd, err
}

// onlyDuologFiles reports whether every file in dir, apart from what
// ignoredInSkill passes over, is one some duolog wrote. A link in it is not.
func onlyDuologFiles(dir string) (bool, error) {
	var files []string
	odd := false
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() || ignoredInSkill(d.Name()):
		case d.Type().IsRegular():
			files = append(files, path)
		default:
			odd = true
		}
		return nil
	})
	if err != nil || odd {
		return false, err
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		if !duologWrote(data) {
			return false, nil
		}
	}
	return true, nil
}

// skillWrites are the writes of every file of the bundled skill name of
// agent into dir. The old files are duolog's own, so no backup is kept.
func skillWrites(agent, name, dir string) ([]edit, error) {
	files, err := skillFiles(agent, name)
	if err != nil {
		return nil, err
	}
	edits := make([]edit, 0, len(files))
	for _, rel := range files {
		data, err := plugin.FS.ReadFile(agent + "/skills/" + name + "/" + rel)
		if err != nil {
			return nil, err
		}
		edits = append(edits, edit{path: filepath.Join(dir, filepath.FromSlash(rel)), data: data, plain: true, restart: true})
	}
	return edits, nil
}

// userSkillEdits plans the writes of agent a's bundled skills at user scope
// under home: each skill that is missing, or whose files some duolog wrote
// and that differs from this binary's, which covers another version. A
// folder that duolog did not write, one the person changed, and one with a
// link stay, and a note names each.
func userSkillEdits(a Agent, home string) ([]edit, []string, error) {
	dir := userSkillDir(a, home)
	if dir == "" {
		return nil, nil, nil
	}
	var edits []edit
	var notes []string
	for _, name := range plugin.Skills {
		folder := filepath.Join(dir, name)
		state, _, err := readUserSkill(a.Name, name, folder)
		if err != nil {
			return nil, nil, err
		}
		switch state {
		case skillCurrent:
			continue
		case skillForeign:
			notes = append(notes, foreignNote(a, folder))
			continue
		case skillEdited:
			notes = append(notes, editedNote(a, folder))
			continue
		}
		writes, err := skillWrites(a.Name, name, folder)
		if err != nil {
			return nil, nil, err
		}
		edits = append(edits, writes...)
	}
	return edits, notes, nil
}

// foreignNote is the note for a user skill folder of duolog's name that
// duolog did not write.
func foreignNote(a Agent, folder string) string {
	return title(a.Name) + ": left " + folder + ": duolog did not write it (its SKILL.md names no " + versionKey + ", or it holds a link); move it away and run duolog install"
}

// editedNote is the note for a user skill folder that duolog wrote and the
// person then changed.
func editedNote(a Agent, folder string) string {
	return title(a.Name) + ": left " + folder + ": it has changes duolog did not write; keep your changes elsewhere, remove the folder and run duolog install to get duolog's skill back"
}

func title(name string) string {
	if name == "" {
		return name
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// RefreshSkills brings duolog's user skills under home up to this binary for
// a lead's session start in the project at root: each skill that an older
// duolog wrote is replaced, so an update of the binary updates the skills.
// A missing skill, one of the same or a newer version, one the person
// changed, one with a link and one duolog did not write stay. An agent whose plugin is enabled for root has the plugin's
// skills and is left alone. note says what changed, and is empty when
// nothing did.
func (d Deps) RefreshSkills(root, home string) (note string, err error) {
	v := Version()
	changed := false
	for _, a := range d.Agents {
		if plugins, ok := a.Installer.(Plugins); ok {
			if on, err := plugins.Enabled(root, home, os.Environ()); err != nil || on {
				continue
			}
		}
		more, err := refreshAgent(a, home, v)
		if err != nil {
			return "", err
		}
		changed = changed || more
	}
	if !changed {
		return "", nil
	}
	return "duolog: updated the duolog skills to " + v, nil
}

// refreshAgent replaces each of agent a's user skills under home that a
// duolog older than version v wrote, and reports whether it replaced any.
func refreshAgent(a Agent, home, v string) (bool, error) {
	dir := userSkillDir(a, home)
	if dir == "" {
		return false, nil
	}
	changed := false
	for _, name := range plugin.Skills {
		folder := filepath.Join(dir, name)
		state, version, err := readUserSkill(a.Name, name, folder)
		if err != nil {
			return changed, err
		}
		if state != skillOld || version != "" && !olderVersion(version, v) {
			continue
		}
		writes, err := skillWrites(a.Name, name, folder)
		if err != nil {
			return changed, err
		}
		for _, e := range writes {
			if _, err := e.write(); err != nil {
				return changed, err
			}
		}
		changed = true
	}
	return changed, nil
}

// removeUserSkills deletes the files of agent a's skills at user scope under
// home that some duolog wrote and that are unchanged. A file the person
// changed or added stays, and so does its folder, with a note. A folder
// duolog did not write, or with a link, is left alone. It reports whether it
// removed any file.
func removeUserSkills(a Agent, home string) (bool, []string, error) {
	dir := userSkillDir(a, home)
	if dir == "" {
		return false, nil, nil
	}
	removed := false
	var notes []string
	for _, name := range plugin.Skills {
		folder := filepath.Join(dir, name)
		state, _, err := readUserSkill(a.Name, name, folder)
		if err != nil {
			return removed, notes, err
		}
		if state != skillCurrent && state != skillOld && state != skillEdited {
			continue
		}
		gone, err := removeOwnFiles(folder)
		removed = removed || gone
		if err != nil {
			return removed, notes, err
		}
		pruneTree(folder)
		if state == skillEdited {
			notes = append(notes, title(a.Name)+": left "+folder+": it has files duolog did not write or that were changed; remove it when you no longer need it")
		}
	}
	return removed, notes, nil
}

// removeOwnFiles deletes from folder the files some duolog wrote, as it wrote
// them. It reports whether it removed any.
func removeOwnFiles(folder string) (bool, error) {
	files, err := copyFiles(folder)
	if err != nil {
		return false, err
	}
	removed := false
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return removed, err
		}
		if !duologWrote(data) {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return removed, err
		}
		removed = true
	}
	return removed, nil
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// projectCopyDirs are the project skills folders of agent a where an earlier
// install may have put duolog's skills: the one the config names and the
// default one. A folder that is also a user skills folder, as a project at
// the home makes it, is not one.
func projectCopyDirs(a Agent, agents []Agent, o Options) []string {
	if o.Root == "" {
		return nil
	}
	var dirs []string
	for _, dir := range []string{a.Installer.SkillDir("project", o.Root, o.Home), defaultSkillDir(a, Options{Scope: "project", Root: o.Root, Home: o.Home}, o.Root)} {
		if dir == "" || containsString(dirs, dir) {
			continue
		}
		user := false
		for _, b := range agents {
			if u := userSkillDir(b, o.Home); u != "" && (u == dir || sameDir(u, dir)) {
				user = true
			}
		}
		if !user {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// projectCopies sorts the folders of duolog's bundled skills in agent a's
// project skills folders: ours hold only files some duolog wrote, so install
// removes them; kept hold other files too, so install leaves them. A folder
// that is, or sits below, a link stays too, and notes name it.
func projectCopies(a Agent, agents []Agent, o Options) (ours, kept, notes []string, err error) {
	for _, dir := range projectCopyDirs(a, agents, o) {
		for _, name := range plugin.Skills {
			skill := filepath.Join(dir, name)
			note, skip := skipSkill(skill, o.Root)
			if note != "" {
				notes = append(notes, note)
			}
			if skip {
				continue
			}
			only, err := onlyDuologFiles(skill)
			if err != nil {
				return nil, nil, nil, err
			}
			if only {
				ours = append(ours, skill)
			} else {
				kept = append(kept, skill)
			}
		}
	}
	return ours, kept, notes, nil
}

// copyFiles lists the regular files in the skill folder skill.
func copyFiles(skill string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(skill, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		files = append(files, path)
		return nil
	})
	return files, err
}

// dropProjectCopies removes the unchanged copies of duolog's skills that an
// earlier install wrote into the project, now that they live at user scope,
// and returns a note for each copy it removed or left.
func dropProjectCopies(agents []Agent, o Options) ([]string, error) {
	var notes []string
	for _, a := range agents {
		ours, kept, more, err := projectCopies(a, agents, o)
		notes = append(notes, more...)
		if err != nil {
			return notes, err
		}
		for _, skill := range ours {
			files, err := copyFiles(skill)
			if err != nil {
				return notes, err
			}
			for _, path := range files {
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					return notes, err
				}
			}
			pruneTree(skill)
			pruneUp(filepath.Dir(skill), o.Root)
			notes = append(notes, "removed "+skill+": duolog keeps its skills at user scope now")
		}
		for _, skill := range kept {
			notes = append(notes, keptNote(skill))
		}
	}
	return notes, nil
}

// keptNote is the note for a project copy of a duolog skill that has files
// duolog did not write.
func keptNote(skill string) string {
	return "left " + skill + ": it has files duolog did not write; duolog keeps its skills at user scope now, so remove this copy when you no longer need it"
}

// serverStamp is the file that install writes when it writes the MCP server
// entry. The entry's own file says nothing by its time: the agent rewrites it
// all the time.
func serverStamp(home string) string {
	return filepath.Join(home, ".duolog", "mcp-server-written")
}

// NewerThan reports whether a user skill file of any agent, or the stamp that
// install writes with the MCP server entry, was written after since. A lead
// whose process started before that has not loaded them. The server file is
// not compared: the agent rewrites it all the time. It stats a few files and
// fails closed: any error is false.
func (d Deps) NewerThan(home string, since time.Time) bool {
	if home == "" {
		return false
	}
	newer := func(path string) bool {
		info, err := os.Stat(path)
		return err == nil && info.ModTime().After(since)
	}
	if newer(serverStamp(home)) {
		return true
	}
	for _, a := range d.Agents {
		if dir := userSkillDir(a, home); dir != "" {
			for _, name := range plugin.Skills {
				if newer(filepath.Join(dir, name, "SKILL.md")) {
					return true
				}
			}
		}
	}
	return false
}
