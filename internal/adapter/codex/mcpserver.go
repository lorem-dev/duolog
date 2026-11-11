package codex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/trusthome"
)

// mcpHead is the start of duolog's MCP server table in Codex's config.toml.
// tool_timeout_sec is mcp.MaxWaitMinutes*60 + 60: a waiting delegate or peer
// call may take the longest wait, and Codex stops a tool call after 60
// seconds by default. The env_vars line follows it (see table).
const mcpHead = "[mcp_servers.duolog]\ncommand = \"duolog\"\nargs = [\"mcp\"]\ntool_timeout_sec = 660\n"

// baseEnvNames are the variables duolog's server reads from the lead, which
// Codex's short default environment for a stdio server lacks: the worker
// and peer markers (mcp.Enabled), the agents' homes, duolog's config, state
// and cache folders, and Rex's session. A detached delegate run that the
// server starts inherits them.
func baseEnvNames() []string {
	return []string{
		"APPDATA", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "DUOLOG_DEPTH", "DUOLOG_PEER", "DUOLOG_TELEMETRY",
		"LOCALAPPDATA", "REX_BIN_DIR", "REX_BLOCK", "REX_SESSION", "SYSTEMROOT", "USERPROFILE",
		"XDG_CACHE_HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME",
	}
}

// ourKeys are the keys of duolog's table that install owns.
var ourKeys = regexp.MustCompile(`^\s*(command|args|tool_timeout_sec|env_vars)\s*=`)

// errNotTable refuses a duolog entry in a shape other than its own
// [mcp_servers.duolog] table: an inline table or dotted keys cannot be cut
// out by their header lines.
var errNotTable = errors.New("mcp_servers.duolog is not a [table]; nothing changed")

// errBadEdit refuses an edit that would not read as the old config plus or
// minus exactly duolog's entry.
var errBadEdit = errors.New("mcp_servers.duolog cannot be edited by its lines; nothing changed")

// WithEnvVars returns i with names, the env_passthrough and paid_runs env
// names of the config at install time, added to env_vars of its server
// table, so that a delegate run the server starts gets them too.
func (i Installer) WithEnvVars(names []string) Installer {
	i.EnvVars = slices.Clone(names)
	return i
}

// envNames are the names of env_vars: baseEnvNames and i.EnvVars, sorted,
// once each, without a name a variable cannot have.
func (i Installer) envNames() []string {
	names := slices.DeleteFunc(append(baseEnvNames(), i.EnvVars...), func(n string) bool { return !envName.MatchString(n) })
	slices.Sort(names)
	return slices.Compact(names)
}

// table is duolog's table as install writes it.
func (i Installer) table() string { return renderTable(i.envNames()) }

// renderTable is duolog's table with env_vars names.
func renderTable(names []string) string {
	quoted := make([]string, len(names))
	for k, n := range names {
		quoted[k] = kit.TOMLString(n)
	}
	return mcpHead + "env_vars = [" + strings.Join(quoted, ", ") + "]\n"
}

// ServerFile is Codex's user config.toml, which lists the MCP servers: the
// one under CODEX_HOME when environ sets it, else under .codex in home, as
// HookFile finds the Codex home at user scope.
func (Installer) ServerFile(home string, environ []string) string {
	environ = append(slices.Clip(environ), "HOME="+home, "USERPROFILE="+home)
	dir, err := kit.Home(environ, "CODEX_HOME", ".codex")
	if err != nil {
		dir = filepath.Join(home, ".codex")
	}
	return filepath.Join(dir, "config.toml")
}

// Allow returns existing: Codex's hooks.json has no allow list, and Codex
// asks the person about an MCP call by its own approval settings.
func (Installer) Allow(existing []byte, _ []string, _ bool) ([]byte, error) {
	return existing, nil
}

// Server adds duolog's [mcp_servers.duolog] table to the config.toml whose
// bytes are existing (nil for none), or removes it with its sub-tables when
// add is false. It edits the text by header lines outside multi-line values,
// so every other byte stays: a new table goes at the end after a blank line,
// and add rewrites the lines of duolog's keys in place when one differs,
// keeping the person's other keys, env_vars names and sub-tables there. An
// entry of that name that does not run duolog mcp is the person's: add and
// remove both leave it and say so in note. The result must read as the old
// config plus or minus exactly duolog's entry; a file that does not parse
// (agent.ErrServerFileUnreadable), an entry that is not a [table], or an
// edit that would change anything else is refused, and nothing changes.
func (i Installer) Server(existing []byte, add bool) (data []byte, note string, err error) {
	if existing == nil {
		if !add {
			return nil, "", nil
		}
		return []byte(i.table()), "", nil
	}
	doc := map[string]any{}
	if err := toml.Unmarshal(existing, &doc); err != nil {
		return nil, "", fmt.Errorf("%w: %v", agent.ErrServerFileUnreadable, err) //nolint:errorlint // one wrapped sentinel; the parse error is text
	}
	entry, found, err := duologEntry(doc)
	switch {
	case err != nil:
		return nil, "", err
	case !found && !add:
		return existing, "", nil
	case !found:
		return checkedEdit(existing, appendTable(existing, i.table()), parsedEntry(i.table()))
	case !oursTable(entry) && add:
		return existing, "mcp_servers.duolog runs something else; rename it", nil
	case !oursTable(entry):
		return existing, "mcp_servers.duolog does not run duolog mcp, so it stays", nil
	}
	return i.editOurs(newConfigText(existing), entry, add)
}

// editOurs is Server for a file whose mcp_servers.duolog is duolog's own
// entry, read as entry.
func (i Installer) editOurs(c configText, entry map[string]any, add bool) ([]byte, string, error) {
	blocks := c.serverBlocks()
	if !slices.ContainsFunc(blocks, func(b serverBlock) bool { return b.main }) {
		return nil, "", errNotTable
	}
	if !add {
		return checkedEdit(c.data, c.remove(blocks), nil)
	}
	names, ok := mergedNames(entry["env_vars"], i.envNames())
	if !ok {
		return nil, "", errors.New("mcp_servers.duolog.env_vars is not a list of names; nothing changed")
	}
	table := renderTable(names)
	want := parsedEntry(table)
	if isCurrent(entry, want, i.envNames()) {
		return c.data, "", nil
	}
	// The rewrite replaces the lines of duolog's keys; every other line of
	// its table and its sub-tables, such as tools.<tool> with an
	// approval_mode, are the person's and stay.
	for k, v := range entry {
		if _, ours := want[k]; !ours {
			want[k] = v
		}
	}
	return checkedEdit(c.data, c.rewrite(blocks, table), want)
}

// mergedNames are the names of env_vars as v holds them, in their order,
// then those of need it lacks, sorted. ok is false when v is set and is not
// a list of names.
func mergedNames(v any, need []string) ([]string, bool) {
	var names []string
	if v != nil {
		list, ok := v.([]any)
		if !ok {
			return nil, false
		}
		for _, item := range list {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			names = append(names, s)
		}
	}
	for _, n := range need {
		if !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	return names, true
}

// isCurrent reports whether entry is duolog's own as want has it: the same
// command, args and tool_timeout_sec, and env_vars with every name of need.
// Any other key is the person's.
func isCurrent(entry, want map[string]any, need []string) bool {
	for _, k := range []string{"command", "args", "tool_timeout_sec"} {
		if !reflect.DeepEqual(entry[k], want[k]) {
			return false
		}
	}
	names, ok := mergedNames(entry["env_vars"], nil)
	if !ok || entry["env_vars"] == nil {
		return false
	}
	for _, n := range need {
		if !slices.Contains(names, n) {
			return false
		}
	}
	return true
}

// duologEntry returns mcp_servers.duolog of doc, and whether it is there. A
// value that is not a table comes back nil: it is not duolog's.
func duologEntry(doc map[string]any) (map[string]any, bool, error) {
	v, ok := doc["mcp_servers"]
	if !ok {
		return nil, false, nil
	}
	servers, ok := v.(map[string]any)
	if !ok {
		return nil, false, errors.New("mcp_servers is not a table, nothing changed")
	}
	entry, ok := servers["duolog"]
	if !ok {
		return nil, false, nil
	}
	table, _ := entry.(map[string]any)
	return table, true, nil
}

// oursTable reports whether a server entry runs duolog mcp, as install
// writes it.
func oursTable(entry map[string]any) bool {
	command, _ := entry["command"].(string)
	args, _ := entry["args"].([]any)
	return command == "duolog" && slices.Equal(args, []any{"mcp"})
}

// parsedEntry is duolog's entry as a parsed table holds it.
func parsedEntry(table string) map[string]any {
	doc := map[string]any{}
	_ = toml.Unmarshal([]byte(table), &doc) // renderTable writes valid TOML
	entry, _, _ := duologEntry(doc)
	return entry
}

// lineEnd is the line end of data: CRLF when it has one, else LF.
func lineEnd(data []byte) string {
	if bytes.Contains(data, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}

// appendTable returns existing with table at its end, after a blank line,
// in the file's own line ends. A file without a final line end gets the
// table without one too, so that removing it gives the file back.
func appendTable(existing []byte, table string) []byte {
	nl := lineEnd(existing)
	table = strings.ReplaceAll(table, "\n", nl)
	if len(existing) == 0 {
		return []byte(table)
	}
	out := slices.Clip(existing)
	if !bytes.HasSuffix(out, []byte("\n")) {
		return append(append(out, nl+nl...), strings.TrimSuffix(table, nl)...)
	}
	return append(append(out, nl...), table...)
}

// configText is config.toml split into lines, with whether each line
// starts outside a multi-line string or array: only such a line can be a
// table header or a key.
type configText struct {
	data  []byte
	lines []configLine
	plain []bool
}

func newConfigText(data []byte) configText {
	c := configText{data: data, lines: configLines(data)}
	c.plain = make([]bool, len(c.lines))
	ml, depth := "", 0
	for k, line := range c.lines {
		c.plain[k] = ml == "" && depth == 0
		if c.plain[k] && strings.HasPrefix(strings.TrimSpace(line.text), "[") {
			continue // a header: its brackets open no value
		}
		ml, depth = scanValue(line.text, ml, depth)
	}
	return c
}

// scanValue reads one line of values and returns the multi-line string it
// leaves open (its three quotes, double or single), and the number of open
// brackets and braces.
func scanValue(s, ml string, depth int) (string, int) {
	for i := 0; i < len(s); {
		if ml != "" {
			i, ml = inMultiLine(s, i, ml)
			continue
		}
		switch c := s[i]; {
		case c == '#':
			return ml, depth
		case strings.HasPrefix(s[i:], `"""`), strings.HasPrefix(s[i:], `'''`):
			ml, i = s[i:i+3], i+3
		case c == '"':
			i = endOfString(s, i+1, '"')
		case c == '\'':
			i = endOfString(s, i+1, '\'')
		case c == '[' || c == '{':
			depth, i = depth+1, i+1
		case (c == ']' || c == '}') && depth > 0:
			depth, i = depth-1, i+1
		default:
			i++
		}
	}
	return ml, depth
}

// inMultiLine steps over byte i of s inside the multi-line string ml: an
// escape of a basic string, the closing quotes with up to two more quotes
// that belong to the string, or one byte. It returns the next index and ml,
// "" once the string closed.
func inMultiLine(s string, i int, ml string) (int, string) {
	switch {
	case ml == `"""` && s[i] == '\\':
		return i + 2, ml
	case strings.HasPrefix(s[i:], ml):
		i += 3
		for extra := 0; extra < 2 && i < len(s) && s[i] == ml[0]; extra++ {
			i++
		}
		return i, ""
	}
	return i + 1, ml
}

// endOfString returns the index after the quote that closes a one-line
// string opened before i; a basic string skips its escapes.
func endOfString(s string, i int, quote byte) int {
	for i < len(s) {
		switch {
		case quote == '"' && s[i] == '\\':
			i += 2
		case s[i] == quote:
			return i + 1
		default:
			i++
		}
	}
	return len(s)
}

// serverBlock is one table of duolog's entry: lines first to last, the
// header and its body up to its last key line or value line, and whether
// line first-1 is blank. main is the [mcp_servers.duolog] table itself; the
// others are its sub-tables.
type serverBlock struct {
	first, last int
	blank, main bool
}

// serverBlocks finds the tables of duolog's entry by their header lines.
func (c configText) serverBlocks() []serverBlock {
	var blocks []serverBlock
	open := false
	for k, line := range c.lines {
		if c.plain[k] {
			if path, ok := headerPath(line.text); ok {
				var b serverBlock
				if b, open = c.openBlock(k, path); open {
					blocks = append(blocks, b)
				}
				continue
			}
		}
		t := strings.TrimSpace(line.text)
		if open && (!c.plain[k] || t != "" && !strings.HasPrefix(t, "#")) {
			blocks[len(blocks)-1].last = k
		}
	}
	return blocks
}

// openBlock starts the block of line k, a header for path, when path is
// duolog's table or one of its sub-tables.
func (c configText) openBlock(k int, path []string) (serverBlock, bool) {
	ours := []string{"mcp_servers", "duolog"}
	main := slices.Equal(path, ours)
	if !main && (len(path) <= 2 || !slices.Equal(path[:2], ours)) {
		return serverBlock{}, false
	}
	blank := k > 0 && c.plain[k-1] && strings.TrimSpace(c.lines[k-1].text) == ""
	return serverBlock{first: k, last: k, blank: blank, main: main}, true
}

// headerPath reads line as a table or array-of-tables header and returns
// its key path.
func headerPath(line string) ([]string, bool) {
	if !strings.HasPrefix(strings.TrimSpace(line), "[") {
		return nil, false
	}
	m := map[string]any{}
	if toml.Unmarshal([]byte(line+"\n"), &m) != nil {
		return nil, false
	}
	var path []string
	for len(m) == 1 {
		for key, v := range m {
			path = append(path, key)
			if list, ok := v.([]any); ok && len(list) == 1 {
				v = list[0]
			}
			next, ok := v.(map[string]any)
			if !ok {
				return nil, false
			}
			m = next
		}
	}
	if len(m) != 0 || len(path) == 0 {
		return nil, false
	}
	return path, true
}

// blankLine reports whether line k is a blank line outside any value.
func (c configText) blankLine(k int) bool {
	return k >= 0 && k < len(c.lines) && c.plain[k] && strings.TrimSpace(c.lines[k].text) == ""
}

// remove takes duolog's tables out, each with the blank line before it. A
// cut that would leave two blank lines in a row, or one at the start, takes
// one more, and a cut at the end of a file without a final line end leaves
// none either.
func (c configText) remove(blocks []serverBlock) []byte {
	var out []byte
	next := 0 // the first line not yet copied
	for _, b := range blocks {
		first := b.first
		if b.blank && b.first-1 >= next {
			first--
		}
		out = append(out, c.span(next, first)...)
		next = b.last + 1
		if (len(out) == 0 || c.blankLine(first-1)) && c.blankLine(next) {
			next++
		}
	}
	out = append(out, c.span(next, len(c.lines))...)
	if next >= len(c.lines) && !bytes.HasSuffix(c.data, []byte("\n")) {
		out = bytes.TrimSuffix(out, []byte(lineEnd(c.data)))
	}
	return out
}

// rewrite puts table in place of the lines of duolog's keys in the main
// table, keeping its other lines and the sub-tables.
func (c configText) rewrite(blocks []serverBlock, table string) []byte {
	var out []byte
	next := 0
	for _, b := range blocks {
		if !b.main {
			continue
		}
		out = append(out, c.span(next, b.first)...)
		out = append(out, strings.ReplaceAll(table, "\n", lineEnd(c.data))...)
		skip := false
		for k := b.first + 1; k <= b.last; k++ {
			if c.plain[k] {
				skip = ourKeys.MatchString(c.lines[k].text)
			}
			if !skip {
				out = append(out, c.span(k, k+1)...)
			}
		}
		next = b.last + 1
	}
	return append(out, c.span(next, len(c.lines))...)
}

// span is the bytes of lines from to to, to excluded.
func (c configText) span(from, to int) []byte {
	if from >= to || from >= len(c.lines) {
		return nil
	}
	return c.data[c.lines[from].start:c.lines[to-1].end]
}

// checkedEdit returns edited when it reads as existing with duolog's entry
// set to entry, or removed when entry is nil, and refuses it otherwise.
func checkedEdit(existing, edited []byte, entry map[string]any) ([]byte, string, error) {
	want, got := map[string]any{}, map[string]any{}
	if toml.Unmarshal(existing, &want) != nil || toml.Unmarshal(edited, &got) != nil {
		return nil, "", errBadEdit
	}
	servers, _ := want["mcp_servers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
		want["mcp_servers"] = servers
	}
	delete(servers, "duolog")
	if entry != nil {
		servers["duolog"] = entry
	}
	for _, doc := range []map[string]any{want, got} {
		if s, ok := doc["mcp_servers"].(map[string]any); ok && len(s) == 0 {
			delete(doc, "mcp_servers")
		}
	}
	if !reflect.DeepEqual(want, got) {
		return nil, "", errBadEdit
	}
	return edited, "", nil
}

// serverOff are the overrides that keep duolog's MCP server from a Codex
// worker or peer: Codex starts a stdio server with a short environment, so
// the server could not tell it from the lead. They turn off each server
// named duolog or running duolog mcp, under any name, that the process's
// config defines (the user config under environ, CODEX_HOME alone if it has
// no home folder, and with root the trusted project's) or that bridged
// defines. Only defined servers: for any other, Codex would read the
// override as a server with no transport and refuse its config. Codex's
// system and managed layers are not read; env_vars still guards there.
func serverOff(root string, environ, bridged []string) []string {
	off := bridgedDuolog(bridged)
	if f, ok := codexFolders(environ); ok {
		merged, _, _ := codexMerged(root, f)
		for name, v := range merged {
			if table, _ := v.(map[string]any); name == "duolog" || runsDuologMCP(table) {
				off[name] = true
			}
		}
	}
	var args []string
	for _, name := range slices.Sorted(maps.Keys(off)) {
		// A name that a bare -c key cannot hold is left to env_vars.
		if codexName.MatchString(name) {
			args = append(args, "-c", "mcp_servers."+name+".enabled=false")
		}
	}
	return args
}

// codexFolders are the folders of a process whose environment is environ,
// as FoldersIn finds them, or with no home folder but a CODEX_HOME, that
// Codex home alone.
func codexFolders(environ []string) (kit.Folders, bool) {
	if f, err := kit.FoldersIn("", environ); err == nil {
		return f, true
	}
	if trusthome.Getenv(environ, "CODEX_HOME") != "" {
		return kit.Folders{Environ: environ}, true
	}
	return kit.Folders{}, false
}

// runsDuologMCP reports whether a server table runs duolog mcp: a command
// whose base name is duolog or duolog.exe, from any folder, and args mcp.
func runsDuologMCP(table map[string]any) bool {
	command, _ := table["command"].(string)
	args, _ := table["args"].([]any)
	base := strings.ToLower(command[strings.LastIndexAny(command, `/\`)+1:])
	return (base == "duolog" || base == "duolog.exe") && slices.Equal(args, []any{"mcp"})
}

// bridgedDuolog names the servers that the -c overrides of bridged define
// and that are named duolog or run duolog mcp.
func bridgedDuolog(bridged []string) map[string]bool {
	tables := map[string]map[string]any{}
	for _, a := range bridged {
		key, value, ok := strings.Cut(a, "=")
		rest, isServer := strings.CutPrefix(key, "mcp_servers.")
		name, field, hasField := strings.Cut(rest, ".")
		if !ok || !isServer || !hasField {
			continue
		}
		if tables[name] == nil {
			tables[name] = map[string]any{}
		}
		var v struct{ V any }
		if toml.Unmarshal([]byte("V = "+value), &v) == nil {
			tables[name][field] = v.V
		}
	}
	off := map[string]bool{}
	for name, table := range tables {
		if name == "duolog" || runsDuologMCP(table) {
			off[name] = true
		}
	}
	return off
}

// WriteServer replaces config.toml at path, whose bytes were old (nil for
// none), with data, as AdoptWorktree writes it: under the config's lock,
// after keeping the first backup, in one rename. A file that changed since
// it was read is left alone. It reports whether it wrote.
func (Installer) WriteServer(path string, old, data []byte) (bool, error) {
	if old != nil && bytes.Equal(old, data) {
		return false, nil
	}
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), configLockWait)
	defer cancel()
	unlock, err := store.LockFileContext(ctx, path+configLockSuffix, 20*time.Millisecond)
	if err != nil {
		return false, fmt.Errorf("lock %s: %w", path, err)
	}
	defer func() { _ = unlock() }()
	return writeLocked(path, old, data)
}

// writeLocked is WriteServer once it holds the lock.
func writeLocked(path string, old, data []byte) (bool, error) {
	changed := fmt.Errorf("%s changed during install; retry", path)
	current, err := store.ReadFile(path)
	switch {
	case os.IsNotExist(err) && old == nil:
		return true, createConfig(path, data)
	case os.IsNotExist(err) || err == nil && !bytes.Equal(current, old) || err == nil && old == nil:
		return false, changed
	case err != nil:
		return false, err
	}
	if _, err := writeConfig(path, old, data); errors.Is(err, errConfigChanged) {
		return false, changed
	} else if err != nil {
		return false, err
	}
	return true, nil
}

// createConfig writes a new config.toml at path, readable by its owner only.
func createConfig(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := writeSynced(tmp, data, 0o600); err != nil {
		return err
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return fmt.Errorf("%s changed during install; retry", path)
	}
	return store.Rename(tmp.Name(), path)
}
