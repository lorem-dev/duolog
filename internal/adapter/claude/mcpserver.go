package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/install"
	"github.com/lorem-dev/duolog/internal/trusthome"
)

// mcpServerName is the name of duolog's MCP server in Claude's config
// (mcp.ServerName), and mcpEntry the entry install writes for it.
const (
	mcpServerName = "duolog"
	mcpEntry      = `{"type":"stdio","command":"duolog","args":["mcp"]}`
)

// toolSearchEnv is Claude Code's setting for deferred tool loading. Tool
// search is on by default; "false" turns it off, so every tool schema loads
// in full each turn.
const toolSearchEnv = "ENABLE_TOOL_SEARCH"

// ServerFile is Claude's user state file, which lists the user-scope MCP
// servers: .claude.json under home, or under CLAUDE_CONFIG_DIR when environ
// sets it. caps reads the servers from the same file.
func (Installer) ServerFile(home string, environ []string) string {
	return trusthome.ClaudeStateIn(home, environ)
}

// Server adds duolog's entry at top-level mcpServers.duolog of the state file
// whose bytes are existing (nil for none), or removes it when add is false.
// It edits the text in place, so every other key keeps its bytes, and
// returns existing itself when nothing changes. An entry of that name that
// does not run duolog mcp is the person's: add and remove both leave it and
// say so in note. Add rewrites duolog's own entry when it differs, and remove
// drops mcpServers when duolog's was its only server. A file that is not a
// JSON object is refused.
func (Installer) Server(existing []byte, add bool) (data []byte, note string, err error) {
	if existing == nil {
		if !add {
			return nil, "", nil
		}
		return []byte("{\n  \"mcpServers\": {\n    \"" + mcpServerName + "\": " + mcpEntry + "\n  }\n}\n"), "", nil
	}
	top, err := scanObject(existing, 0)
	if err != nil {
		return nil, "", fmt.Errorf("not a JSON object, nothing changed: %w", err)
	}
	i := top.find("mcpServers")
	if i < 0 {
		if !add {
			return existing, "", nil
		}
		value := "{\n" + top.indent(existing) + "  \"" + mcpServerName + "\": " + mcpEntry + "\n" + top.indent(existing) + "}"
		return top.insert(existing, "mcpServers", value), "", nil
	}
	servers, err := scanObject(existing, top.members[i].value)
	if err != nil {
		return nil, "", fmt.Errorf("mcpServers is not an object, nothing changed: %w", err)
	}
	data, note = editServers(existing, top, i, servers, add)
	return data, note, nil
}

// editServers is Server for a file whose top-level object top has
// mcpServers, read as servers, as its member i.
func editServers(existing []byte, top jsonObject, i int, servers jsonObject, add bool) ([]byte, string) {
	j := servers.find(mcpServerName)
	if j < 0 {
		if add {
			return servers.insert(existing, mcpServerName, mcpEntry), ""
		}
		return existing, ""
	}
	raw := servers.raw(existing, j)
	switch {
	case !oursEntry(raw) && add:
		return existing, "mcpServers.duolog runs something else; rename it"
	case !oursEntry(raw):
		return existing, "mcpServers.duolog does not run duolog mcp, so it stays"
	case add && sameEntry(raw, []byte(mcpEntry)):
		return existing, ""
	case add:
		return servers.replace(existing, j, mcpEntry), ""
	case len(servers.members) == 1:
		return top.remove(existing, i), ""
	}
	return servers.remove(existing, j), ""
}

// sameEntry reports whether two server entries hold the same values.
func sameEntry(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

// oursEntry reports whether a server entry runs duolog mcp, as install
// writes it.
func oursEntry(raw []byte) bool {
	var e struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	return json.Unmarshal(raw, &e) == nil && e.Command == "duolog" && slices.Equal(e.Args, []string{"mcp"})
}

// Allow adds entries to permissions.allow of the settings file whose bytes
// are existing (nil for none), or removes them when add is false, keeping
// every other setting and entry. It returns existing itself when nothing
// changes, and otherwise the file as install writes settings.
func (Installer) Allow(existing []byte, entries []string, add bool) ([]byte, error) {
	top := map[string]json.RawMessage{}
	if existing != nil {
		if err := json.Unmarshal(existing, &top); err != nil || top == nil {
			return nil, fmt.Errorf("not a JSON object, nothing changed: %v", err) //nolint:errorlint // err is nil when top is null
		}
	}
	permissions := map[string]json.RawMessage{}
	if raw, ok := top["permissions"]; ok {
		if err := json.Unmarshal(raw, &permissions); err != nil || permissions == nil {
			return nil, fmt.Errorf("bad \"permissions\" value, nothing changed: %v", err) //nolint:errorlint // err is nil when the value is null
		}
	}
	var allow []json.RawMessage
	if raw, ok := permissions["allow"]; ok {
		if err := json.Unmarshal(raw, &allow); err != nil {
			return nil, fmt.Errorf("bad \"permissions.allow\" value, nothing changed: %w", err)
		}
	}
	allow, changed := editAllow(allow, entries, add)
	if !changed {
		return existing, nil
	}
	if err := setOrDrop(permissions, "allow", allow, len(allow) == 0); err != nil {
		return nil, err
	}
	if err := setOrDrop(top, "permissions", permissions, len(permissions) == 0); err != nil {
		return nil, err
	}
	return install.Encode(top)
}

// editAllow adds the entries that allow lacks, or drops those it has, and
// reports whether it changed. Entries that are not strings stay.
func editAllow(allow []json.RawMessage, entries []string, add bool) ([]json.RawMessage, bool) {
	text := func(raw json.RawMessage) string {
		var s string
		_ = json.Unmarshal(raw, &s)
		return s
	}
	if !add {
		kept := slices.DeleteFunc(slices.Clone(allow), func(raw json.RawMessage) bool { return slices.Contains(entries, text(raw)) })
		return kept, len(kept) != len(allow)
	}
	changed := false
	for _, e := range entries {
		if !slices.ContainsFunc(allow, func(raw json.RawMessage) bool { return text(raw) == e }) {
			raw, _ := json.Marshal(e)
			allow, changed = append(allow, raw), true
		}
	}
	return allow, changed
}

// setOrDrop sets m[key] to v as JSON, or deletes the key when drop is true.
func setOrDrop[V any](m map[string]json.RawMessage, key string, v V, drop bool) error {
	if drop {
		delete(m, key)
		return nil
	}
	raw, err := json.Marshal(v)
	m[key] = raw
	return err
}

// DeferredOff reports whether a Claude lead at root, with home and the
// environment environ, has deferred tool loading off: ENABLE_TOOL_SEARCH is
// false in its environment or in the env of a settings file it reads, the
// last file Claude applies winning.
func (Installer) DeferredOff(root, home string, environ []string) bool {
	value := trusthome.Getenv(environ, toolSearchEnv)
	for _, path := range install.ClaudeSettingsFilesIn(root, home, environ) {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var settings struct {
			Env map[string]any `json:"env"`
		}
		if json.Unmarshal(data, &settings) != nil {
			continue
		}
		if v, ok := settings.Env[toolSearchEnv]; ok {
			value = fmt.Sprint(v)
		}
	}
	return strings.EqualFold(strings.TrimSpace(value), "false")
}

// jsonObject is the layout of one JSON object in a file: where it opens and
// closes, and where each member's key and value are.
type jsonObject struct {
	open, close int // offsets of { and }
	members     []member
}

// member is one key of an object: start is the offset of its key's opening
// quote, value and end bound its value.
type member struct {
	key               string
	start, value, end int
}

// scanObject reads the layout of the JSON object at offset at of data. At
// zero, data must be one JSON value and nothing more.
func scanObject(data []byte, at int) (jsonObject, error) {
	if at == 0 && !json.Valid(data) {
		return jsonObject{}, errors.New("the file does not parse")
	}
	dec := json.NewDecoder(bytes.NewReader(data[at:]))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return jsonObject{}, errors.New("not an object")
	}
	o := jsonObject{open: at + int(dec.InputOffset()) - 1}
	for dec.More() {
		m := member{start: at + int(dec.InputOffset())}
		for isSpace(data[m.start]) || data[m.start] == ',' {
			m.start++
		}
		tok, err := dec.Token()
		if err != nil {
			return jsonObject{}, err
		}
		m.key, _ = tok.(string)
		keyEnd := at + int(dec.InputOffset())
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return jsonObject{}, err
		}
		m.value = keyEnd + bytes.IndexByte(data[keyEnd:], ':') + 1
		for isSpace(data[m.value]) {
			m.value++
		}
		m.end = m.value + len(raw)
		if !bytes.Equal(data[m.value:m.end], raw) {
			return jsonObject{}, errors.New("unexpected layout")
		}
		o.members = append(o.members, m)
	}
	if _, err := dec.Token(); err != nil {
		return jsonObject{}, err
	}
	o.close = at + int(dec.InputOffset()) - 1
	return o, nil
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

// find returns the index of the member named key, or -1.
func (o jsonObject) find(key string) int {
	return slices.IndexFunc(o.members, func(m member) bool { return m.key == key })
}

// raw returns the value bytes of member i.
func (o jsonObject) raw(data []byte, i int) []byte {
	return data[o.members[i].value:o.members[i].end]
}

// indent is the indentation of the object's members: that of its first
// member's line, or of its own line plus two spaces when it has none.
func (o jsonObject) indent(data []byte) string {
	if len(o.members) > 0 {
		if ind, ok := lineIndent(data, o.members[0].start); ok {
			return ind
		}
		return ""
	}
	line := bytes.LastIndexByte(data[:o.open], '\n') + 1
	end := line
	for end < o.open && (data[end] == ' ' || data[end] == '\t') {
		end++
	}
	return string(data[line:end]) + "  "
}

// lineIndent returns the blanks before offset at on its line, and false when
// something else comes before it there.
func lineIndent(data []byte, at int) (string, bool) {
	line := bytes.LastIndexByte(data[:at], '\n') + 1
	ind := data[line:at]
	return string(ind), line > 0 && len(bytes.Trim(ind, " \t")) == 0
}

// insert adds "key": value as the object's last member.
func (o jsonObject) insert(data []byte, key, value string) []byte {
	ind := o.indent(data)
	member := `"` + key + `": ` + value
	if len(o.members) == 0 {
		return splice(data, o.open+1, o.close, "\n"+ind+member+"\n"+strings.TrimSuffix(ind, "  "))
	}
	sep := ", "
	if _, ok := lineIndent(data, o.members[0].start); ok {
		sep = ",\n" + ind
	}
	last := o.members[len(o.members)-1].end
	return splice(data, last, last, sep+member)
}

// replace puts value in place of member i's value.
func (o jsonObject) replace(data []byte, i int, value string) []byte {
	return splice(data, o.members[i].value, o.members[i].end, value)
}

// remove deletes member i with the separator before it, or after it when it
// is the first, so an insert followed by a remove gives the bytes back.
func (o jsonObject) remove(data []byte, i int) []byte {
	switch {
	case len(o.members) == 1:
		return splice(data, o.open+1, o.close, "")
	case i > 0:
		return splice(data, o.members[i-1].end, o.members[i].end, "")
	}
	return splice(data, o.members[0].start, o.members[1].start, "")
}

// splice returns data with data[from:to] replaced by text.
func splice(data []byte, from, to int, text string) []byte {
	out := make([]byte, 0, len(data)+len(text))
	out = append(out, data[:from]...)
	out = append(out, text...)
	return append(out, data[to:]...)
}
