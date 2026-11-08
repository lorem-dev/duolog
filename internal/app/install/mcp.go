package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/lorem-dev/duolog/internal/store"
)

// MCPServers is implemented by the Installer of an agent whose lead gets the
// duolog MCP server. Run adds the server and the allow entries, Remove takes
// them out, Check compares them.
type MCPServers interface {
	// ServerFile is the agent's user-scope file that lists MCP servers.
	ServerFile(home string, environ []string) string
	// Server adds (add true) or removes duolog's entry in the bytes of that
	// file, keeping every other key byte for byte. Both leave an entry under
	// duolog's name that is not duolog's and say so in note.
	Server(existing []byte, add bool) (data []byte, note string, err error)
	// Allow adds or removes entries in permissions.allow of a settings file,
	// keeping every other setting.
	Allow(existing []byte, entries []string, add bool) ([]byte, error)
}

// DeferredLoading is implemented by the MCPServers of an agent that can load
// every tool schema in full each turn. DeferredOff reports that it does for
// a lead at root under home whose environment is environ.
type DeferredLoading interface {
	DeferredOff(root, home string, environ []string) bool
}

// mcpAllowed are the allow entries install adds beside the server, as
// mcp.AllowedTools names them. answer_ask is never among them: the lead's
// guard or the person decides it.
var mcpAllowed = []string{"mcp__duolog__delegate", "mcp__duolog__peer", "mcp__duolog__status"}

// ServerRemovedNote is what uninstall says when it removes duolog's server:
// the entry is at user scope, so every project loses it.
const ServerRemovedNote = "duolog MCP server removed for all projects; other duolog projects get it back with duolog install"

// DeferredOffLine is doctor's line for a lead that loads every schema of
// duolog's server in full each turn.
const DeferredOffLine = "duolog MCP: deferred tool loading is off (ENABLE_TOOL_SEARCH=false), so its four tools load in full every turn; turn it on or run duolog uninstall to drop the server"

// planMCP adds the MCP writes of agent a to p: the server entry in the
// agent's user state file and the allow entries in the hooks or settings
// file of the scope, on top of the hooks p writes there, with the record of
// the entries it adds (see allowRecord). An agent without a server is left
// alone.
func planMCP(p *agentPlan, a Agent, o Options, base string) error {
	servers, ok := a.Installer.(MCPServers)
	if !ok {
		return nil
	}
	if path, ok := mcpServerFile(servers, o.Home); ok {
		old, err := readIfExists(path)
		if err != nil {
			return err
		}
		data, note, err := servers.Server(old, true)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if note != "" {
			p.notes = append(p.notes, path+": "+note)
		}
		p.mcp = []edit{{path: path, data: data, state: true, restart: true}}
	}
	// Without a hook edit the file is missing: Run writes one for the plugin
	// whenever the file is there.
	var settings []byte
	if p.hook != nil {
		settings = p.hook.data
	}
	record, err := recordAdded(servers, allowRecord(a, o, base), settings)
	if err != nil {
		return fmt.Errorf("%s: %w", p.hookFile, err)
	}
	if record != nil {
		p.mcp = append(p.mcp, *record)
	}
	settings, err = servers.Allow(settings, mcpAllowed, true)
	if err != nil {
		return fmt.Errorf("%s: %w", p.hookFile, err)
	}
	p.hook = &edit{path: p.hookFile, data: settings}
	return nil
}

// allowRecord is the file that lists the allow entries install added to
// agent a's hooks or settings file at o's scope under base, so that
// uninstall removes those and leaves the ones the person had. A project
// keeps it in .duolog/, which Git ignores; the user scope keeps it beside
// the settings file, as no duolog folder belongs in the home.
func allowRecord(a Agent, o Options, base string) string {
	if o.Scope == "user" {
		return filepath.Join(filepath.Dir(a.Installer.HookFile("user", base, o.Home)), "duolog-allow-entries.json")
	}
	return filepath.Join(base, ".duolog", a.Name+"-allow-entries.json")
}

// readAllowRecord reads the entries the record at path lists; a missing
// record lists none.
func readAllowRecord(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []string
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return entries, nil
}

// recordAdded plans the record at path of the allow entries that install
// adds to the settings bytes settings, on top of those it recorded before.
// An entry that is there already is the person's and is not recorded. It
// returns nil when the record stays as it is.
func recordAdded(servers MCPServers, path string, settings []byte) (*edit, error) {
	had, err := readAllowRecord(path)
	if err != nil {
		return nil, err
	}
	var entries []string
	for _, e := range mcpAllowed {
		with, err := servers.Allow(settings, []string{e}, true)
		if err != nil {
			return nil, err
		}
		if slices.Contains(had, e) || !bytes.Equal(with, settings) {
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 || slices.Equal(entries, had) {
		return nil, nil
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return nil, err
	}
	return &edit{path: path, data: append(data, '\n'), state: true}, nil
}

// mcpServerFile is the agent's state file under home that lists its MCP
// servers, and false without a home: that file is the person's, never one
// under the working directory.
func mcpServerFile(servers MCPServers, home string) (string, bool) {
	if home == "" {
		return "", false
	}
	return servers.ServerFile(home, os.Environ()), true
}

// dropAllow takes the allow entries that install added, as the record at
// path lists them, out of the bytes of agent a's hooks or settings file.
// Without a record it takes none: they cannot be told from the person's.
func dropAllow(a Agent, data []byte, record string) ([]byte, error) {
	servers, ok := a.Installer.(MCPServers)
	if !ok {
		return data, nil
	}
	added, err := readAllowRecord(record)
	if err != nil || len(added) == 0 {
		return data, err
	}
	// Only duolog's own entries, whatever else the file lists.
	added = slices.DeleteFunc(added, func(e string) bool { return !slices.Contains(mcpAllowed, e) })
	return servers.Allow(data, added, false)
}

// dropAllowRecords deletes the records of added allow entries of agents at
// o's scope under base, once uninstall has taken those entries out.
func dropAllowRecords(agents []Agent, o Options, base string) error {
	for _, a := range agents {
		if _, ok := a.Installer.(MCPServers); !ok {
			continue
		}
		if err := os.Remove(allowRecord(a, o, base)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// serverRemoval plans the removal of duolog's server entry of agent a, with
// the note to print: the server goes for every project, and an entry that
// is not duolog's stays.
func serverRemoval(a Agent, o Options) ([]edit, []string, error) {
	servers, ok := a.Installer.(MCPServers)
	if !ok {
		return nil, nil, nil
	}
	path, ok := mcpServerFile(servers, o.Home)
	if !ok {
		return nil, nil, nil
	}
	old, err := readIfExists(path)
	if err != nil || old == nil {
		return nil, nil, err
	}
	data, note, err := servers.Server(old, false)
	switch {
	case err != nil:
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	case note != "":
		return nil, []string{path + ": " + note}, nil
	case bytes.Equal(data, old):
		return nil, nil, nil
	}
	return []edit{{path: path, data: data, state: true}}, []string{ServerRemovedNote}, nil
}

// checkMCP adds to s the MCP files that Run would change for agent a: the
// server file when duolog's entry is missing or different, and the hooks or
// settings file when an allow entry is missing. Allow entries alone make an
// install. It leaves a check that failed, runs the dev launcher, or compares
// the hooks alone.
func checkMCP(s *Status, a Agent, o Options) {
	servers, ok := a.Installer.(MCPServers)
	if !ok || s.Dev || s.Err != nil || o.HooksOnly {
		return
	}
	settings, err := readIfExists(s.HookFile)
	if err != nil {
		s.Err = err
		return
	}
	added, err := servers.Allow(settings, mcpAllowed, true)
	if err != nil {
		s.Err = fmt.Errorf("%s: %w", s.HookFile, err)
		return
	}
	dropped, _ := servers.Allow(settings, mcpAllowed, false) // it parsed above
	if !s.Installed && bytes.Equal(dropped, settings) {
		return
	}
	s.Installed = true
	if !bytes.Equal(added, settings) && !slices.Contains(s.Changed, s.HookFile) {
		s.Changed = append(s.Changed, s.HookFile)
	}
	path, ok := mcpServerFile(servers, o.Home)
	if !ok {
		return
	}
	switch changed, foreign, err := serverChanged(servers, path); {
	case err != nil:
		s.Err = fmt.Errorf("%s: %w", path, err)
	case foreign:
		s.ForeignServer = path
	case changed:
		s.Changed = append(s.Changed, path)
	}
}

// serverChanged reports whether Run would change duolog's entry in the
// server file at path, as it is missing or different, and whether the entry
// under duolog's name is the person's, which Run leaves.
func serverChanged(servers MCPServers, path string) (changed, foreign bool, err error) {
	old, err := readIfExists(path)
	if err != nil {
		return false, false, err
	}
	data, note, err := servers.Server(old, true)
	if err != nil {
		return false, false, err
	}
	return !bytes.Equal(data, old), note != "", nil
}

// deferredLines are doctor's lines for the agents whose server duolog has
// installed and whose lead at root loads every tool schema in full.
func (d Deps) deferredLines(root, home string) []string {
	if home == "" {
		return nil
	}
	environ := os.Environ()
	for _, a := range d.Agents {
		servers, ok := a.Installer.(MCPServers)
		deferred, ok2 := a.Installer.(DeferredLoading)
		if !ok || !ok2 || !deferred.DeferredOff(root, home, environ) {
			continue
		}
		old, err := os.ReadFile(servers.ServerFile(home, environ))
		if err != nil {
			continue
		}
		// The entry is duolog's and current: add changes nothing and says
		// nothing about an entry of the person's.
		if data, note, err := servers.Server(old, true); err == nil && note == "" && bytes.Equal(data, old) {
			return []string{DeferredOffLine}
		}
	}
	return nil
}

// write writes e: through a backup as apply does, or, for an agent's state
// file, in place.
func (e edit) write() (bool, error) {
	if e.state {
		return writeState(e.path, e.data)
	}
	return apply(e)
}

// writeState replaces the file at path with data, atomically: a temporary
// file in the same folder, renamed over it. The file keeps its mode, a link
// stays a link, and no backup is kept: an agent's state file is large and
// the agent rewrites it often.
func writeState(path string, data []byte) (bool, error) {
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	mode := os.FileMode(0o600)
	old, err := os.ReadFile(path)
	switch {
	case err == nil && bytes.Equal(old, data):
		return false, nil
	case err == nil:
		info, err := os.Stat(path)
		if err != nil {
			return false, err
		}
		mode = info.Mode().Perm()
	case !os.IsNotExist(err):
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(data)
	if closeErr := tmp.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return false, writeErr
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return false, err
	}
	return true, store.Rename(tmp.Name(), path)
}
