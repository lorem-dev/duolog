package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// CompactVar is the Claude variable that sets the context size, in tokens,
// at which a session compacts. Workers get it in their --settings env block;
// the lead gets it in the env block of the project's local settings.
const CompactVar = "CLAUDE_CODE_AUTO_COMPACT_WINDOW"

// LeadEnv is implemented by the Installer of an agent whose lead install sets
// the compact window: Claude. LocalSettings is the project's local,
// git-ignored settings file at root, which install writes. OtherSettings
// lists every other settings file a lead at root reads, with home and the
// environment environ, from the lowest precedence to the highest.
type LeadEnv interface {
	LocalSettings(root string) string
	OtherSettings(root, home string, environ []string) []string
}

// LeadWindow is the compact window of a lead: Window tokens, 0 for Claude's
// own default, for Plan, the Claude plan the quota read named ("" when it
// named none and Window is the fallback).
type LeadWindow struct {
	Window int
	Plan   string
}

// leadMarker is what install records of the value it wrote, in
// .duolog/lead-compact.json, which Git ignores. Window 0 records that install
// chose Claude's own default and wrote nothing. Created says install created
// the local settings file, so uninstall may remove it once it is empty.
type leadMarker struct {
	Window  int    `json:"window"`
	Plan    string `json:"plan,omitempty"`
	Created bool   `json:"created,omitempty"`
}

// leadState is what the files say of a lead's compact window.
type leadState struct {
	local  string
	doc    map[string]json.RawMessage
	value  string
	set    bool
	marker *leadMarker
	// ours says the local value is the one install wrote.
	ours bool
	// own names where the person set the variable, a settings file or the
	// environment, and ownValue its value; own is "" when they set none.
	own, ownValue string
}

// LeadStatus says how the lead's compact window compares with what install
// writes. Own names where the person set the variable themselves, and Value
// is the value that applies; Missing says install has not set the window
// (or the person's own value) here yet, which Run would fix.
type LeadStatus struct {
	File, Own, Value, Plan string
	Window                 int
	Missing                bool
}

// line is what install --check and doctor say of the lead, with paths under
// root shown relative to it.
func (l LeadStatus) line(agent, root string) string {
	head := agent + " lead: "
	switch {
	case l.Own != "":
		return head + CompactVar + "=" + l.Value + " is set in " + shown(root, []string{l.Own}) + "; duolog keeps it"
	case l.Missing:
		return ""
	case l.Window == 0:
		return head + "compacts at Claude's own default: workers.compact_window gives 0" + planText(l.Plan)
	}
	return head + "compacts at " + strconv.Itoa(l.Window) + " tokens" + planText(l.Plan) + ", set by duolog in " + shown(root, []string{l.File})
}

func planText(plan string) string {
	if plan == "" {
		return " (plan not read: the smallest ladder value)"
	}
	return " for plan " + plan
}

func markerPath(root string) string { return filepath.Join(root, ".duolog", "lead-compact.json") }

// readLead reads the lead's compact window at root from the local settings
// file, install's marker, the other settings files and environ. The
// environment counts as the person's only when no settings file sets the
// variable: a settings value applies over it, and a lead's shell holds the
// value of its settings.
func readLead(e LeadEnv, root, home string, environ []string) (leadState, error) {
	st := leadState{local: e.LocalSettings(root), doc: map[string]json.RawMessage{}}
	data, err := os.ReadFile(st.local)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &st.doc); err != nil || st.doc == nil {
			return st, fmt.Errorf("%s: not a JSON object, nothing changed", st.local)
		}
		st.value, st.set = envValue(st.doc)
	case !os.IsNotExist(err):
		return st, err
	}
	st.marker = readMarker(root)
	st.ours = st.set && st.marker != nil && st.marker.Window > 0 && st.value == strconv.Itoa(st.marker.Window)
	switch {
	case st.set && !st.ours:
		st.own, st.ownValue = st.local, st.value
	default:
		st.own, st.ownValue = settingsValue(e.OtherSettings(root, home, environ))
	}
	if st.own == "" && !st.set {
		st.own, st.ownValue = environValue(environ)
	}
	return st, nil
}

// readMarker reads install's marker at root, nil when there is none or it
// cannot be read.
func readMarker(root string) *leadMarker {
	data, err := os.ReadFile(markerPath(root))
	if err != nil {
		return nil
	}
	var m leadMarker
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	return &m
}

// settingsValue finds the variable in the env blocks of paths, lowest
// precedence first: the last file that sets it, and its value. A file that is
// missing or not JSON sets none.
func settingsValue(paths []string) (where, value string) {
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var doc map[string]json.RawMessage
		if json.Unmarshal(data, &doc) != nil {
			continue
		}
		if v, ok := envValue(doc); ok {
			where, value = path, v
		}
	}
	return where, value
}

// environValue finds the variable in environ: "the environment" and its
// value, or "" when it is not set.
func environValue(environ []string) (where, value string) {
	for _, kv := range environ {
		if v, ok := strings.CutPrefix(kv, CompactVar+"="); ok {
			where, value = "the environment", v
		}
	}
	return where, value
}

// envValue reads the variable from the env block of a settings document.
func envValue(doc map[string]json.RawMessage) (string, bool) {
	var env map[string]any
	if json.Unmarshal(doc["env"], &env) != nil {
		return "", false
	}
	v, ok := env[CompactVar]
	if !ok {
		return "", false
	}
	if s, isText := v.(string); isText {
		return s, true
	}
	return fmt.Sprint(v), true
}

// withValue returns the settings document doc with the variable set to value
// in its env block, or removed when value is "". Other keys stay; an empty
// env block goes.
func withValue(doc map[string]json.RawMessage, value string) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	for k, v := range doc {
		out[k] = v
	}
	env := map[string]json.RawMessage{}
	if raw, ok := doc["env"]; ok {
		if err := json.Unmarshal(raw, &env); err != nil || env == nil {
			return nil, errors.New(`bad "env" value, nothing changed`)
		}
	}
	if value == "" {
		delete(env, CompactVar)
	} else {
		env[CompactVar], _ = json.Marshal(value)
	}
	if len(env) == 0 {
		delete(out, "env")
		return out, nil
	}
	raw, err := encode(env)
	if err != nil {
		return nil, err
	}
	out["env"] = raw
	return out, nil
}

// encode indents v as a settings file, without HTML escaping.
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

// leadAgents are the selected agents whose lead install sets the window.
func leadAgents(agents []Agent) []Agent {
	var out []Agent
	for _, a := range agents {
		if _, ok := a.Installer.(LeadEnv); ok {
			out = append(out, a)
		}
	}
	return out
}

// environ is the environment install reads the person's own value in.
func (d Deps) environ() []string {
	if d.Environ != nil {
		return d.Environ
	}
	return os.Environ()
}

// installLead sets the compact window of the lead of each selected agent
// that has one, in project scope: the window of the person's plan in the
// local settings, recorded in the marker. A value the person set, in that
// file, another settings file or the environment, stays, and the notes say
// so; a value install wrote earlier is then removed, since the local file
// would apply over theirs. Window 0 writes nothing.
func (d Deps) installLead(agents []Agent, o Options) ([]string, []string, error) {
	if d.LeadWindow == nil || o.Scope == "user" {
		return nil, nil, nil
	}
	var files, notes []string
	for _, a := range leadAgents(agents) {
		e, _ := a.Installer.(LeadEnv)
		st, err := readLead(e, o.Root, o.Home, d.environ())
		if err != nil {
			return files, notes, err
		}
		w := LeadWindow{}
		if st.own == "" {
			if w, err = d.LeadWindow(o.Root); err != nil {
				notes = append(notes, a.Name+" lead: compact window not set: "+err.Error())
				continue
			}
		}
		changed, err := writeLead(st, o.Root, w)
		if err != nil {
			return files, notes, err
		}
		files = append(files, changed...)
		status := LeadStatus{File: st.local, Own: st.own, Value: st.ownValue, Window: w.Window, Plan: w.Plan}
		notes = append(notes, status.line(a.Name, o.Root))
	}
	return files, notes, nil
}

// writeLead brings the local settings and the marker to what st and w ask:
// the person's own value, when st has one, with install's earlier value and
// marker gone; otherwise w's window, or none for 0, recorded in the marker.
// It returns the files it changed.
func writeLead(st leadState, root string, w LeadWindow) ([]string, error) {
	var files []string
	value := ""
	if st.own == "" && w.Window > 0 {
		value = strconv.Itoa(w.Window)
	}
	created := st.marker != nil && st.marker.Created
	if st.ours || value != "" {
		_, statErr := os.Stat(st.local)
		created = created || os.IsNotExist(statErr)
		changed, err := setLocal(st, root, value, created)
		if err != nil {
			return nil, err
		}
		if changed {
			files = append(files, st.local)
		}
	}
	if st.own != "" {
		if err := os.Remove(markerPath(root)); err != nil && !os.IsNotExist(err) {
			return files, err
		}
		return files, nil
	}
	data, err := json.Marshal(leadMarker{Window: w.Window, Plan: w.Plan, Created: created && value != ""})
	if err != nil {
		return files, err
	}
	if err := os.MkdirAll(filepath.Dir(markerPath(root)), 0o755); err != nil {
		return files, err
	}
	return files, os.WriteFile(markerPath(root), append(data, '\n'), 0o644)
}

// setLocal writes value, or removes the variable for "", in the local
// settings of st. A file install created that is left empty goes.
func setLocal(st leadState, root, value string, created bool) (bool, error) {
	doc, err := withValue(st.doc, value)
	if err != nil {
		return false, fmt.Errorf("%s: %w", st.local, err)
	}
	if len(doc) == 0 && created {
		err := os.Remove(st.local)
		if os.IsNotExist(err) {
			return false, nil
		}
		return err == nil, err
	}
	data, err := encode(doc)
	if err != nil {
		return false, err
	}
	return apply(edit{path: st.local, data: data, backup: projectBackup(root, st.local)})
}

// removeLead takes out of the local settings the value install wrote, and
// the marker. A value the person set, or changed, stays.
func removeLead(agents []Agent, root string) error {
	if _, err := os.Stat(markerPath(root)); err != nil {
		// No marker: install wrote no value here.
		return nil //nolint:nilerr // a marker that cannot be read leaves every value alone
	}
	for _, a := range leadAgents(agents) {
		e, _ := a.Installer.(LeadEnv)
		st, err := readLead(e, root, "", nil)
		if err != nil {
			return err
		}
		if st.ours {
			if _, err := setLocal(st, root, "", st.marker.Created); err != nil {
				return err
			}
		}
		if err := os.Remove(markerPath(root)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// checkLead compares the lead's compact window of agent a with what Run
// writes, from the files alone: it reads no quota. Nil means the agent sets
// none, or the check is of user scope or hooks alone.
func (d Deps) checkLead(a Agent, o Options) (*LeadStatus, error) {
	e, ok := a.Installer.(LeadEnv)
	if !ok || d.LeadWindow == nil || o.HooksOnly || o.Scope == "user" {
		return nil, nil
	}
	st, err := readLead(e, o.Root, o.Home, d.environ())
	if err != nil {
		return nil, err
	}
	l := &LeadStatus{File: st.local, Own: st.own, Value: st.ownValue}
	if st.own != "" {
		return l, nil
	}
	m := st.marker
	l.Missing = m == nil || m.Window > 0 && !st.ours || m.Window == 0 && st.set
	if m != nil {
		l.Window, l.Plan = m.Window, m.Plan
	}
	return l, nil
}

// addLead adds the lead's compact window of agent a to s, for an install that
// could be compared and does not run the dev launcher.
func (d Deps) addLead(s *Status, a Agent, o Options) {
	if s.Err != nil || s.Dev {
		return
	}
	o.Scope = s.Scope
	s.Lead, s.Err = d.checkLead(a, o)
}

// LeadLine is what doctor says of the lead's compact window in the project
// at root: the window duolog set, or the person's own value. It is empty
// when no lead window is checked, or the window is missing, which the
// install lines name.
func (d Deps) LeadLine(root, home string) string {
	statuses, err := d.Check(Options{Scope: "project", Root: root, Home: home})
	if err != nil {
		return ""
	}
	for _, s := range statuses {
		if s.Lead != nil && s.Err == nil {
			return s.Lead.line(s.Agent, root)
		}
	}
	return ""
}
