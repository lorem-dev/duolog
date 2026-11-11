package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// StatusLines is implemented by the Installer of an agent whose status line
// duolog sets: Claude. StatusLine adds duolog's status line to the settings
// file whose bytes are existing, or with add false removes it, and returns
// existing itself when nothing changes; a status line of the person is never
// replaced or removed. OurStatusLine says whether data holds duolog's status
// line, and whether it holds any. An entry whose command is
// "duolog statusline" counts as duolog's, whoever wrote it.
type StatusLines interface {
	StatusLine(existing []byte, add bool) ([]byte, error)
	OurStatusLine(data []byte) (ours, has bool)
}

// StatusLineState says how an agent's status line compares with what install
// writes. File is the settings file install writes it to. Own names the
// settings file that holds a status line of the person, "" when there is none.
// Missing says install has not set the status line yet, which Run would fix.
// Declined says the person removed the status line install wrote, so install
// leaves it off until Options.StatusLine asks for it. Hides says duolog's
// status line in File applies over the person's in Own, which Run would fix.
type StatusLineState struct {
	File, Own         string
	Missing, Declined bool
	Hides             bool
}

// statusLineFile is where the status line goes. Project scope uses the local
// settings: a status line is the person's, and the committed settings would
// hide a user status line for every teammate. User scope uses the user
// settings file.
func statusLineFile(a Agent, o Options) (string, bool) {
	if o.Scope == "user" {
		return a.Installer.HookFile("user", o.Root, o.Home), true
	}
	e, ok := a.Installer.(LeadEnv)
	if !ok {
		return "", false
	}
	return e.LocalSettings(o.Root), true
}

// statusLineAgents are the agents in agents whose status line install sets.
func statusLineAgents(agents []Agent) []Agent {
	var out []Agent
	for _, a := range agents {
		if _, ok := a.Installer.(StatusLines); ok {
			out = append(out, a)
		}
	}
	return out
}

// slMarker is what install records of the status line in
// .duolog/statusline.json, which Git ignores: Created says install created
// the local settings file, so that uninstall may remove it once it is empty;
// Wrote says install wrote the entry, so that an entry that is gone later was
// removed by the person; Declined says they did, and install leaves it off.
type slMarker struct {
	Created  bool `json:"created,omitempty"`
	Wrote    bool `json:"wrote,omitempty"`
	Declined bool `json:"declined,omitempty"`
}

func statusLineMarkerPath(root string) string {
	return filepath.Join(root, ".duolog", "statusline.json")
}

// readSLMarker reads the marker at root, empty when there is none. Project
// scope only: user scope keeps none.
func readSLMarker(root string) slMarker {
	var m slMarker
	if data, err := os.ReadFile(statusLineMarkerPath(root)); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	return m
}

func writeSLMarker(root string, m slMarker) error {
	path := statusLineMarkerPath(root)
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// statusLineRead is what the files say of an agent's status line.
type statusLineRead struct {
	file string
	data []byte
	// ours says the file holds duolog's status line; own names a settings file
	// that holds the person's.
	ours bool
	own  string
	// marker is install's marker; zero at user scope.
	marker slMarker
}

func (d Deps) readStatusLine(a Agent, o Options) (statusLineRead, error) {
	file, ok := statusLineFile(a, o)
	if !ok {
		return statusLineRead{}, nil
	}
	sl, _ := a.Installer.(StatusLines)
	st := statusLineRead{file: file}
	if o.Scope != "user" {
		st.marker = readSLMarker(o.Root)
	}
	data, err := readIfExists(file)
	if err != nil {
		return st, err
	}
	st.data = data
	if data != nil {
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(data, &doc); err != nil || doc == nil {
			return st, fmt.Errorf("%s: not a JSON object, nothing changed", file)
		}
	}
	ours, has := sl.OurStatusLine(data)
	st.ours = ours
	if has && !ours {
		st.own = file
		return st, nil
	}
	if o.Scope == "user" {
		return st, nil
	}
	// A settings file with a higher precedence than the others wins, so the
	// last one that holds a status line of the person is the one shown.
	if e, ok := a.Installer.(LeadEnv); ok {
		for _, path := range e.OtherSettings(o.Root, o.Home, d.environ()) {
			other, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			if theirs, has := sl.OurStatusLine(other); has && !theirs {
				st.own = path
			}
		}
	}
	return st, nil
}

// declined says the person removed the status line install wrote: the marker
// says so, or says install wrote it and it is gone.
func (st statusLineRead) declined() bool {
	return st.own == "" && !st.ours && (st.marker.Declined || st.marker.Wrote)
}

// installStatusLine sets the status line of each selected agent that has one:
// duolog's own in the settings file of the scope, when the person has none
// anywhere and did not remove it. A status line of the person stays, a note
// says how to add duolog's part, and one install wrote earlier is removed,
// since it would apply over theirs. One the person removed stays off, unless
// o.StatusLine asks for it. It is written also when the agent's plugin
// supplies the hooks.
func (d Deps) installStatusLine(agents []Agent, o Options) ([]string, []string, error) {
	var files, notes []string
	for _, a := range statusLineAgents(agents) {
		st, err := d.readStatusLine(a, o)
		if err != nil {
			return files, notes, err
		}
		if st.file == "" {
			continue
		}
		var changed bool
		switch {
		case st.own != "":
			notes = append(notes, a.Name+": your status line in "+shown(o.Root, []string{st.own})+" stays; to add duolog's part, run duolog statusline with the same stdin")
			if st.ours {
				changed, err = setStatusLine(a, st, o, false, false)
			}
		case st.ours:
			err = d.recordWrote(o, st)
		case st.declined() && !o.StatusLine:
			if !st.marker.Declined && o.Scope != "user" {
				st.marker.Declined = true
				err = writeSLMarker(o.Root, st.marker)
			}
		default:
			changed, err = setStatusLine(a, st, o, true, false)
		}
		if err != nil {
			return files, notes, err
		}
		if changed {
			files = append(files, st.file)
		}
	}
	return files, notes, nil
}

// recordWrote records in the marker that duolog's status line is there, and
// clears a decline the person undid by adding it back.
func (d Deps) recordWrote(o Options, st statusLineRead) error {
	if o.Scope == "user" || st.marker.Wrote && !st.marker.Declined {
		return nil
	}
	st.marker.Wrote, st.marker.Declined = true, false
	return writeSLMarker(o.Root, st.marker)
}

// setStatusLine adds duolog's status line to the settings file of st, or with
// add false removes it, and records it in the marker. A file install created
// that is left empty goes, unless it changed since st read it; leadCreated
// says the lead marker of an older install created it.
func setStatusLine(a Agent, st statusLineRead, o Options, add, leadCreated bool) (bool, error) {
	sl, _ := a.Installer.(StatusLines)
	data, err := sl.StatusLine(st.data, add)
	if err != nil {
		return false, fmt.Errorf("%s: %w", st.file, err)
	}
	if bytes.Equal(data, st.data) {
		return false, nil
	}
	if o.Scope == "user" {
		return apply(edit{path: st.file, data: data})
	}
	// The marker follows the file write, so that a write that failed is not
	// read as the person's decline on the next run.
	m := st.marker
	var changed bool
	if !add && emptyDocument(data) && (m.Created || leadCreated) {
		if changed, err = removeIfUnchanged(st); err != nil || !changed {
			return changed, err
		}
		// The file is gone, and so is what install wrote or created.
		m.Created = false
	} else if changed, err = apply(edit{path: st.file, data: data, backup: projectBackup(o.Root, st.file)}); err != nil {
		return changed, err
	}
	if add {
		m.Created = m.Created || st.data == nil
		m.Wrote, m.Declined = true, false
	} else {
		m.Wrote = false
	}
	return changed, writeSLMarker(o.Root, m)
}

// removeIfUnchanged removes the file of st, unless its content is not what st
// read: someone wrote to it since, and it stays.
func removeIfUnchanged(st statusLineRead) (bool, error) {
	cur, err := os.ReadFile(st.file)
	if err != nil || !bytes.Equal(cur, st.data) {
		return false, nil //nolint:nilerr // a file that cannot be read or has changed stays
	}
	if err := os.Remove(st.file); err != nil {
		return false, err
	}
	return true, nil
}

func emptyDocument(data []byte) bool {
	var doc map[string]json.RawMessage
	return json.Unmarshal(data, &doc) == nil && len(doc) == 0
}

// removeStatusLine takes duolog's status line out of the settings file of
// each selected agent, and install's marker. A status line of the person
// stays. leadCreated says the lead marker of an older install created the
// local settings file.
func (d Deps) removeStatusLine(agents []Agent, o Options, leadCreated bool) ([]string, error) {
	var files []string
	for _, a := range statusLineAgents(agents) {
		st, err := d.readStatusLine(a, o)
		if err != nil {
			return files, err
		}
		if st.file == "" || !st.ours {
			continue
		}
		changed, err := setStatusLine(a, st, o, false, leadCreated)
		if err != nil {
			return files, err
		}
		if changed {
			files = append(files, st.file)
		}
	}
	if o.Scope != "user" {
		if err := os.Remove(statusLineMarkerPath(o.Root)); err != nil && !os.IsNotExist(err) {
			return files, err
		}
	}
	return files, nil
}

// checkStatusLine compares the status line of agent a with what Run writes,
// from the files alone. Nil means the agent has none, or the check is of
// hooks alone.
func (d Deps) checkStatusLine(a Agent, o Options) (*StatusLineState, error) {
	if _, ok := a.Installer.(StatusLines); !ok || o.HooksOnly {
		return nil, nil
	}
	st, err := d.readStatusLine(a, o)
	if err != nil || st.file == "" {
		return nil, err
	}
	declined := st.declined()
	return &StatusLineState{
		File: st.file, Own: st.own, Declined: declined,
		Missing: st.own == "" && !st.ours && !declined,
		Hides:   st.own != "" && st.ours,
	}, nil
}

// addStatusLine adds the status line of agent a to s, for an install that
// could be compared and does not run the dev launcher.
func (d Deps) addStatusLine(s *Status, a Agent, o Options) {
	if s.Err != nil || s.Dev {
		return
	}
	o.Scope = s.Scope
	s.StatusLine, s.Err = d.checkStatusLine(a, o)
}

// StatusLineLine is what install --check says of the status line when it
// needs nothing from install: the person's own, or duolog's left off at their
// request. It is empty for any other.
func (s Status) StatusLineLine(root string) string {
	l := s.StatusLine
	if !s.Installed || s.Err != nil || l == nil || l.Hides {
		return ""
	}
	switch {
	case l.Own != "":
		return s.Agent + ": status line is yours (" + shown(root, []string{l.Own}) + "); run duolog statusline with the same stdin to add duolog's part"
	case l.Declined:
		return s.Agent + ": duolog's status line is off, since you removed it; run duolog install --status-line to add it again"
	}
	return ""
}

// StatusLineLine is what doctor says of the status line in the project at
// root; see Status.StatusLineLine.
func (d Deps) StatusLineLine(root, home string) string {
	statuses, err := d.Check(Options{Scope: "project", Root: root, Home: home})
	if err != nil {
		return ""
	}
	for _, s := range statuses {
		if line := s.StatusLineLine(root); line != "" {
			return line
		}
	}
	return ""
}
