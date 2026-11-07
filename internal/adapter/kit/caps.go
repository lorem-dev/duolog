package kit

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/caps"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/trusthome"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// Folders is where one agent process finds its settings: its home folder,
// and the environment whose CLAUDE_CONFIG_DIR and CODEX_HOME select the
// folders under it.
type Folders struct {
	Home    string
	Environ []string
}

// FoldersIn are the folders of a process whose environment is environ. home,
// when set, replaces the home folder; tests set it. An environment without a
// home folder is trusthome.ErrNoHome.
func FoldersIn(home string, environ []string) (Folders, error) {
	if home != "" {
		return Folders{home, environ}, nil
	}
	h, err := trusthome.HomeIn(environ)
	return Folders{h, environ}, err
}

// Claude is the Claude config folder under f.
func (f Folders) Claude() string { return trusthome.ClaudeIn(f.Home, f.Environ) }

// ClaudeState is Claude's user state file under f.
func (f Folders) ClaudeState() string { return trusthome.ClaudeStateIn(f.Home, f.Environ) }

// Codex is the Codex home under f.
func (f Folders) Codex() string { return trusthome.CodexIn(f.Home, f.Environ) }

// ScanInventory reads the lists of agent from files and from one cheap
// command, for an agent process whose environment is environ: a worker's
// inventory reads the home, CLAUDE_CONFIG_DIR and CODEX_HOME the worker gets,
// and the command runs in that environment. home replaces the home folder when
// set. The result is cached under stateRoot/.duolog/cache, stateRoot being the
// state root of the project. A missing binary gives an unknown inventory, and
// so does an environment that names no home folder: nothing could be claimed
// about what it lacks. scan reads the agent's own files under the folders and
// says which kinds its lists leave incomplete.
func ScanInventory(agent, stateRoot, home string, environ []string, scan func(f Folders) (items []caps.Item, unknownKinds []string)) (caps.Inventory, error) {
	inv := caps.Inventory{Agent: agent, Items: []caps.Item{}}
	if _, err := exec.LookPath(agent); err != nil {
		inv.Unknown = true
		return inv, nil //nolint:nilerr // a missing agent binary is an unknown inventory, not a failure
	}
	f, err := FoldersIn(home, environ)
	if errors.Is(err, trusthome.ErrNoHome) {
		inv.Unknown = true
		return inv, nil
	}
	if err != nil {
		return inv, err
	}
	items, unknown := scan(f)
	inv.UnknownKinds = unknown
	inv.Items = caps.Normalize(items)
	path := filepath.Join(stateRoot, ".duolog", "cache", "inventory-"+agent+".json")
	// An uninitialized project gets no cache, so that a read-only command does not
	// create .duolog/ with no .gitignore, which hooks would then treat as set up.
	if _, statErr := os.Stat(filepath.Join(stateRoot, ".duolog", ".gitignore")); statErr != nil {
		return inv, nil //nolint:nilerr // no cache in an uninitialized project, as the comment above says
	}
	if data, err := json.Marshal(inv); err == nil {
		if os.MkdirAll(filepath.Dir(path), 0755) == nil {
			if f, err := os.CreateTemp(filepath.Dir(path), ".inventory-*"); err == nil {
				_, werr := f.Write(data)
				cerr := f.Close()
				if werr == nil && cerr == nil {
					_ = store.Rename(f.Name(), path)
				}
				_ = os.Remove(f.Name())
			}
		}
	}
	return inv, nil
}

// CachedInventory never invokes a program or refreshes a list on the lead
// hook path: it reads the inventory the last scan of agent cached under
// stateRoot, the state root of the project.
func CachedInventory(agent, stateRoot string) caps.Inventory {
	inv := caps.Inventory{Agent: agent, Unknown: true}
	var cached caps.Inventory
	if ReadJSON(filepath.Join(stateRoot, ".duolog", "cache", "inventory-"+agent+".json"), &cached) && cached.Agent == agent {
		return cached
	}
	return inv
}

// SkillsIn lists <dir>/<name>/SKILL.md entries.
func SkillsIn(dir string) []caps.Item {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var items []caps.Item
	for _, e := range entries {
		path := filepath.Join(dir, e.Name(), "SKILL.md")
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		items = append(items, caps.Item{Kind: caps.KindSkill, Name: e.Name(), Source: path})
	}
	return items
}

// ReadJSON reads the JSON file at path into into; false when it cannot.
func ReadJSON(path string, into any) bool {
	data, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(data, into) == nil
}

// ServerItems are unmarked mcp items for the servers, so that a scan lists
// every server the agent's config layers define, and its cache holds them.
func ServerItems(servers map[string]caps.Server) []caps.Item {
	var items []caps.Item
	for _, name := range SortedNames(servers) {
		items = append(items, caps.Item{Kind: caps.KindMCP, Name: name})
	}
	return items
}

// SortedNames are the names of servers, sorted.
func SortedNames(servers map[string]caps.Server) []string {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Taken is the environment the worker already gets, by name.
func Taken(workerEnv []string) map[string]string {
	taken := map[string]string{}
	for _, kv := range workerEnv {
		k, v, _ := strings.Cut(kv, "=")
		taken[k] = v
	}
	return taken
}

// Allowed is the duolog list for the server, else the lead's own Codex list.
func Allowed(s caps.Server, tools map[string][]string) []string {
	if list, ok := tools[s.Name]; ok {
		return list
	}
	return s.EnabledTools
}

// ClaudeRef matches ${VAR} and ${VAR:-default}, the two forms Claude expands.
var ClaudeRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}`)

// Expand resolves Claude references with lookup, the lead's environment. An
// unset variable with no default fails: Claude would send the text ${VAR}
// instead.
func Expand(text string, lookup func(string) (string, bool)) (string, error) {
	var missing string
	out := ClaudeRef.ReplaceAllStringFunc(text, func(ref string) string {
		m := ClaudeRef.FindStringSubmatch(ref)
		if v, ok := lookup(m[1]); ok {
			return v
		}
		if m[2] != "" {
			return m[3]
		}
		missing = m[1]
		return ""
	})
	if missing != "" {
		return "", fmt.Errorf("it reads $%s, which the lead does not have", missing)
	}
	return out, nil
}

// Builder hands the values of one server to a worker: every env and header
// value goes to the worker's environment under a generated name, and the
// config refers to it by that name, so no value reaches a file or argv.
type Builder struct {
	Server caps.Server
	// Lookup reads the lead's environment.
	Lookup func(string) (string, bool)
	// Windows says whether variable names ignore case, as on Windows, where
	// the later of Path and PATH wins.
	Windows bool
	taken   map[string]string
	env     map[string]string
	order   []string
	n       int
}

// NewBuilder starts the values of s for a worker whose environment is taken.
func NewBuilder(s caps.Server, taken map[string]string, lookup func(string) (string, bool), windows bool) *Builder {
	return &Builder{Server: s, Lookup: lookup, Windows: windows, taken: taken, env: map[string]string{}}
}

var notAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// Secret stores value under a new DUOLOG_MCP_<SERVER>_<N> name and returns it.
func (b *Builder) Secret(value string) string {
	base := "DUOLOG_MCP_" + strings.ToUpper(notAlnum.ReplaceAllString(b.Server.Name, "_"))
	for {
		b.n++
		name := fmt.Sprintf("%s_%d", base, b.n)
		if _, used := EnvGet(b.taken, name, b.Windows); used {
			continue
		}
		if _, used := EnvGet(b.env, name, b.Windows); used {
			continue
		}
		b.set(name, value)
		return name
	}
}

func (b *Builder) set(name, value string) {
	b.env[name] = value
	b.order = append(b.order, name)
}

// Keep passes a stdio variable under its own name, which the server reads. A
// name the worker already has with another value, or one of duolog's, fails.
func (b *Builder) Keep(name, value string) error {
	if err := ReservedName(name); err != nil {
		return err
	}
	if AgentVariable(name) {
		// The variable would reach the worker's agent too: OPENAI_BASE_URL
		// would send its model traffic, and its token, to the server's host.
		return fmt.Errorf("it sets $%s, which the worker's agent reads too", name)
	}
	if old, ok := EnvGet(b.taken, name, b.Windows); ok {
		if old != value {
			return fmt.Errorf("it sets $%s, which the worker already has with another value", name)
		}
		// The worker has it already; its shell keeps it too.
		return nil
	}
	if _, ok := EnvGet(b.env, name, b.Windows); !ok {
		b.set(name, value)
	}
	return nil
}

// Value resolves one config value as the lead's agent would read it.
func (b *Builder) Value(v string) (string, error) {
	if b.Server.Agent == "claude" {
		return Expand(v, b.Lookup)
	}
	return v, nil
}

// Commit gives the values of a server that was bridged as NAME=value pairs
// for the worker's environment, and counts them as taken for the next server.
func (b *Builder) Commit() []string {
	var env []string
	for _, k := range b.order {
		b.taken[k] = b.env[k]
		env = append(env, k+"="+b.env[k])
	}
	return env
}

// ReservedName refuses a server variable that duolog keeps for itself, in
// either direction: the server would get the lead's Rex session or a depth
// that says it is not a worker.
func ReservedName(name string) error {
	if workerenv.Reserved(name) {
		return fmt.Errorf("it sets $%s, a name duolog keeps for itself", name)
	}
	return nil
}

// SecretURL reports a url that may carry a credential in itself.
func SecretURL(raw string) bool {
	u, err := url.Parse(raw)
	return err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != ""
}

// TOMLString writes a TOML basic string. TOML has no \x escape, so control
// characters use \u.
func TOMLString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// TOMLArray writes a TOML array of strings.
func TOMLArray(list []string) string {
	parts := make([]string, len(list))
	for i, s := range list {
		parts[i] = TOMLString(s)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// TOMLTable writes an inline TOML table of strings, sorted by key.
func TOMLTable(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = TOMLString(k) + "=" + TOMLString(m[k])
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// Request returns the url and headers that the agent sends to an HTTP server,
// with every reference read from the lead's environment through lookup. It is
// for a check of the credentials only; the result must not be logged or
// stored.
func Request(s caps.Server, lookup func(string) (string, bool)) (string, map[string]string, error) {
	if s.Transport != "http" {
		return "", nil, fmt.Errorf("it is not an HTTP server")
	}
	b := &Builder{Server: s, Lookup: lookup}
	u, err := b.Value(s.URL)
	if err != nil {
		return "", nil, err
	}
	headers := map[string]string{}
	for _, p := range s.Headers {
		v, err := b.Value(p.Value)
		if err != nil {
			return "", nil, err
		}
		headers[p.Key] = v
	}
	for _, p := range s.EnvHeaders {
		if v, ok := lookup(p.Value); ok {
			headers[p.Key] = v
		}
	}
	if s.BearerVar != "" {
		v, ok := lookup(s.BearerVar)
		if !ok {
			return "", nil, fmt.Errorf("it reads $%s, which the lead does not have", s.BearerVar)
		}
		headers["Authorization"] = "Bearer " + v
	}
	return u, headers, nil
}

// EnvGet looks a variable up by name, ignoring case where windows says the
// system does, so that a server cannot set duolog_depth or Path past the
// worker's own.
func EnvGet(m map[string]string, name string, windows bool) (string, bool) {
	if v, ok := m[name]; ok || !windows {
		return v, ok
	}
	for k, v := range m {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}

// AgentVariable reports a name that an agent, its runtime or the system reads,
// which a bridged stdio server must not set for the whole worker.
func AgentVariable(name string) bool {
	upper := strings.ToUpper(name)
	for _, prefix := range []string{"CODEX_", "OPENAI_", "ANTHROPIC_", "CLAUDE_", "SSL_", "LD_", "DYLD_", "NODE_", "GIT_"} {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	switch upper {
	case "HOME", "PATH", "USERPROFILE", "APPDATA", "XDG_CONFIG_HOME", "TMPDIR", "SHELL", "RUST_LOG", "NO_PROXY":
		return true
	}
	return strings.HasSuffix(upper, "_PROXY")
}
