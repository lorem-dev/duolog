package capability

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/lorem-dev/duolog/internal/install"
	"github.com/lorem-dev/duolog/internal/trust"
	"github.com/pelletier/go-toml/v2"
)

// Server is one MCP server definition from an agent's config file. It stays in
// memory: env and header values leave this package only as worker environment.
type Server struct {
	Name, Agent string
	// File and Scope say where the definition is, for a hint that names the
	// entry that holds a token. They never hold a value.
	File, Scope string
	// Transport is stdio or http; anything else cannot be bridged.
	Transport string
	Command   string
	Args      []string
	Cwd       string
	// Env is the stdio environment. Claude values may hold ${VAR} references.
	Env []Pair
	// EnvVars are Codex env_vars: names whose values come from the lead.
	EnvVars []string
	URL     string
	// Headers are sent as written; Claude values may hold ${VAR} references.
	Headers []Pair
	// EnvHeaders are Codex env_http_headers: header name and variable name.
	EnvHeaders []Pair
	// BearerVar is Codex bearer_token_env_var.
	BearerVar string
	// EnabledTools and DisabledTools are Codex tool filters.
	EnabledTools, DisabledTools []string
	// OAuth is set when the agent signs in to the server itself.
	OAuth bool
	// Why says why the server cannot be bridged; empty means it can.
	Why string
	// Inactive means the agent itself would not start the server: the user has
	// not approved it or has turned it off. duolog sends it nothing, not even a
	// login check.
	Inactive bool
}

// Pair is one name and value, kept in file order.
type Pair struct{ Key, Value string }

// Static reports whether the server has a fixed credential in config, so that
// a rejected request means the token is wrong, not that a login is missing.
func (s Server) Static() bool {
	return len(s.Headers) > 0 || len(s.EnvHeaders) > 0 || s.BearerVar != ""
}

// Servers reads the MCP servers that agent defines in its config files for the
// project at root, with the agent's own precedence. A disabled Codex server is
// left out, as Codex does not start it.
// They are the servers of duolog's own environment, the lead's; ServersEnv
// reads a worker's.
func Servers(agent, root string) map[string]Server {
	return ServersEnv(agent, root, os.Environ())
}

// ServersEnv is Servers for an agent process whose environment is environ:
// the config files are those under its home folder, CLAUDE_CONFIG_DIR and
// CODEX_HOME. An environment without a home folder gives none.
func ServersEnv(agent, root string, environ []string) map[string]Server {
	f, err := foldersIn(environ)
	if err != nil {
		return nil
	}
	return serversIn(agent, root, f)
}

func serversIn(agent, root string, f folders) map[string]Server {
	switch agent {
	case "claude":
		return claudeServers(root, f)
	case "codex":
		return codexServers(root, f)
	}
	return nil
}

type claudeProject struct {
	MCP                map[string]json.RawMessage `json:"mcpServers"`
	Enabled            []string                   `json:"enabledMcpjsonServers"`
	Disabled           []string                   `json:"disabledMcpjsonServers"`
	DisabledMCP        []string                   `json:"disabledMcpServers"`
	TrustDialogPassed  bool                       `json:"hasTrustDialogAccepted"`
	EnableAllMCPServer *bool                      `json:"enableAllProjectMcpServers"`
}

// claudeServers applies Claude's precedence: local, then project, then user.
// The whole entry of the winning scope is used; fields are not merged.
func claudeServers(root string, f folders) map[string]Server {
	var state struct {
		MCP      map[string]json.RawMessage `json:"mcpServers"`
		Projects map[string]claudeProject   `json:"projects"`
	}
	statePath := f.claudeState()
	readJSON(statePath, &state)
	project := state.Projects[root]
	if local, ok := state.Projects[filepath.ToSlash(root)]; ok && project.MCP == nil {
		project = local
	}
	out := make(map[string]Server)
	for name, raw := range state.MCP {
		out[name] = parseClaude(name, "user", statePath+" mcpServers", raw)
	}
	var file struct {
		MCP map[string]json.RawMessage `json:"mcpServers"`
	}
	mcpJSON := filepath.Join(root, ".mcp.json")
	readJSON(mcpJSON, &file)
	for name, raw := range file.MCP {
		s := parseClaude(name, "project", mcpJSON, raw)
		if !claudeApproved(name, root, f, project) {
			// Claude asks before it starts a server from a project file; a
			// server the user never approved must not start in another agent,
			// and it does not hide the user's server of the same name.
			if _, ok := out[name]; ok {
				continue
			}
			s.Inactive = true
			if s.Why == "" {
				s.Why = "Claude has not approved this project server"
			}
		}
		out[name] = s
	}
	for name, raw := range project.MCP {
		out[name] = parseClaude(name, "local", statePath+" projects[root].mcpServers", raw)
	}
	for _, name := range project.DisabledMCP {
		if s, ok := out[name]; ok {
			s.Why = "it is disabled in Claude for this project"
			s.Inactive = true
			out[name] = s
		}
	}
	return out
}

// claudeApproved reads the approval of a .mcp.json server. The settings files in
// the project count only after the folder was trusted, as in Claude.
func claudeApproved(name, root string, f folders, project claudeProject) bool {
	all := project.EnableAllMCPServer != nil && *project.EnableAllMCPServer
	enabled := slices.Contains(project.Enabled, name)
	disabled := slices.Contains(project.Disabled, name)
	for _, path := range install.ClaudeSettingsFilesIn(root, f.home, f.environ) {
		// Both settings files in the project are the project's own, so they
		// approve nothing until the person trusted the folder.
		if strings.HasPrefix(path, filepath.Join(root, ".claude")+string(filepath.Separator)) && !project.TrustDialogPassed {
			continue
		}
		var s struct {
			All      *bool    `json:"enableAllProjectMcpServers"`
			Enabled  []string `json:"enabledMcpjsonServers"`
			Disabled []string `json:"disabledMcpjsonServers"`
		}
		if !readJSON(path, &s) {
			continue
		}
		if s.All != nil {
			all = *s.All
		}
		enabled = enabled || slices.Contains(s.Enabled, name)
		disabled = disabled || slices.Contains(s.Disabled, name)
	}
	return !disabled && (all || enabled)
}

func parseClaude(name, scope, file string, raw json.RawMessage) Server {
	s := Server{Name: name, Agent: "claude", Scope: scope, File: file}
	var def struct {
		Type          string            `json:"type"`
		Command       string            `json:"command"`
		Args          []string          `json:"args"`
		Env           map[string]string `json:"env"`
		URL           string            `json:"url"`
		Headers       map[string]string `json:"headers"`
		HeadersHelper string            `json:"headersHelper"`
		OAuth         json.RawMessage   `json:"oauth"`
	}
	if err := json.Unmarshal(raw, &def); err != nil {
		s.Why = "its definition cannot be read"
		return s
	}
	s.Command, s.Args, s.URL = def.Command, def.Args, def.URL
	s.Env, s.Headers = pairs(def.Env), pairs(def.Headers)
	switch def.Type {
	case "", "stdio":
		s.Transport = "stdio"
		if s.Command == "" {
			s.Why = "it has no command"
		}
	case "http", "streamable-http":
		s.Transport = "http"
		switch {
		case len(def.OAuth) > 0:
			s.OAuth, s.Why = true, "it signs in with OAuth"
		case def.HeadersHelper != "":
			s.Why = "a helper command makes its headers"
		case s.URL == "":
			s.Why = "it has no url"
		case len(s.Headers) == 0:
			// Claude signs in to such a server with OAuth when it asks; a public
			// server looks the same, and there is nothing to hand over.
			s.OAuth, s.Why = true, "it has no credentials in config; it may sign in with OAuth"
		}
	case "sse":
		s.Transport, s.Why = "sse", "it uses SSE, which Codex does not support"
	default:
		s.Transport, s.Why = def.Type, "its transport "+def.Type+" is unknown"
	}
	return s
}

// codexServers reads config.toml in CODEX_HOME, then the project's
// .codex/config.toml when Codex trusts the folder. A project value replaces the
// user value of the same key, table by table, as Codex merges its layers.
func codexServers(root string, f folders) map[string]Server {
	merged, files, user := codexMerged(root, f)
	out := make(map[string]Server)
	for name, value := range merged {
		table, _ := value.(map[string]any) // codexMerged stores only tables
		if enabled, ok := table["enabled"].(bool); ok && !enabled {
			continue
		}
		scope := "user"
		if files[name] != user {
			scope = "project"
		}
		out[name] = parseCodex(name, scope, files[name]+" [mcp_servers."+name+"]", table)
	}
	return out
}

// codexMerged returns the merged mcp_servers tables, disabled ones too, with the
// file that set each last, and the path of the user file.
func codexMerged(root string, f folders) (merged map[string]any, files map[string]string, user string) {
	user = filepath.Join(f.codex(), "config.toml")
	merged, files = map[string]any{}, map[string]string{}
	layers := []string{user}
	if trusted, err := trust.FolderInEnv(f.home, root, f.environ); err == nil && trusted {
		layers = append(layers, filepath.Join(root, ".codex", "config.toml"))
	}
	for _, path := range layers {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var doc struct {
			MCP map[string]any `toml:"mcp_servers"`
		}
		if toml.Unmarshal(data, &doc) != nil {
			continue
		}
		for name, value := range doc.MCP {
			table, ok := value.(map[string]any)
			if !ok {
				continue
			}
			old, _ := merged[name].(map[string]any)
			merged[name] = mergeTables(old, table)
			files[name] = path
		}
	}
	return merged, files, user
}

// CodexOff returns -c overrides that turn off every enabled MCP server in the
// Codex config for root. Codex reads a -c key as parts split at each dot, with
// no TOML quoting, so the name goes in bare: mcp_servers."x".enabled would name
// a new server `"x"` with no transport, and Codex would refuse its config. A
// name that a bare key cannot hold is left out, as the bridge leaves it out.
func CodexOff(root string) []string {
	return CodexOffEnv(root, os.Environ())
}

// CodexOffEnv is CodexOff for a Codex worker whose environment is environ:
// the servers turned off are those its own CODEX_HOME defines.
func CodexOffEnv(root string, environ []string) []string {
	var names []string
	for name := range ServersEnv("codex", root, environ) {
		if codexName.MatchString(name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	var args []string
	for _, name := range names {
		args = append(args, "-c", "mcp_servers."+name+".enabled=false")
	}
	return args
}

// codexHas reports whether the worker's Codex config under worker has a table
// for name, even a disabled one: -c keys would merge into it.
func codexHas(root, name string, worker folders) bool {
	merged, _, _ := codexMerged(root, worker)
	_, ok := merged[name]
	return ok
}

func mergeTables(dst, src map[string]any) map[string]any {
	out := make(map[string]any, len(dst)+len(src))
	for k, v := range dst {
		out[k] = v
	}
	for k, v := range src {
		a, aok := out[k].(map[string]any)
		b, bok := v.(map[string]any)
		if aok && bok {
			out[k] = mergeTables(a, b)
		} else {
			out[k] = v
		}
	}
	return out
}

func parseCodex(name, scope, file string, t map[string]any) Server {
	s := Server{Name: name, Agent: "codex", Scope: scope, File: file}
	str := func(key string) string { v, _ := t[key].(string); return v }
	s.Command, s.URL, s.Cwd, s.BearerVar = str("command"), str("url"), str("cwd"), str("bearer_token_env_var")
	s.Args = stringList(t["args"])
	s.Env = pairs(stringMap(t["env"]))
	s.Headers = pairs(stringMap(t["http_headers"]))
	s.EnvHeaders = pairs(stringMap(t["env_http_headers"]))
	s.EnabledTools, s.DisabledTools = stringList(t["enabled_tools"]), stringList(t["disabled_tools"])
	if list, ok := t["env_vars"].([]any); ok {
		for _, entry := range list {
			switch e := entry.(type) {
			case string:
				s.EnvVars = append(s.EnvVars, e)
			case map[string]any:
				n, _ := e["name"].(string)
				if source, _ := e["source"].(string); source != "" && source != "local" {
					s.Why = "it reads variables from a remote executor"
				}
				s.EnvVars = append(s.EnvVars, n)
			}
		}
	}
	switch {
	case s.Command != "" && s.URL == "":
		s.Transport = "stdio"
	case s.URL != "" && s.Command == "":
		s.Transport = "http"
	default:
		s.Transport = "unknown"
		s.Why = "its transport is unknown"
		return s
	}
	_, hasOAuth := t["oauth"]
	_, hasScopes := t["scopes"]
	_, hasResource := t["oauth_resource"]
	switch {
	case s.Why != "":
	case str("experimental_environment") == "remote":
		s.Why = "it runs on a remote executor"
	case str("http_headers_helper") != "":
		s.Why = "a helper command makes its headers"
	case hasOAuth || hasScopes || hasResource || str("auth") != "":
		s.OAuth, s.Why = true, "it signs in with OAuth"
	case s.Transport == "http" && !s.Static():
		// Codex falls back to stored OAuth credentials for such a server.
		s.OAuth, s.Why = true, "it has no credentials in config; Codex signs in with OAuth"
	}
	return s
}

func pairs(m map[string]string) []Pair {
	out := make([]Pair, 0, len(m))
	for k, v := range m {
		out = append(out, Pair{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func stringMap(v any) map[string]string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, value := range m {
		switch x := value.(type) {
		case string:
			out[k] = x
		default:
			out[k] = fmt.Sprint(x)
		}
	}
	return out
}

func stringList(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// codexShellPolicy reads the worker's shell_environment_policy, as codexServers
// reads its servers. Codex refuses filters next to the older exclude or
// include_only keys, so a policy in the older form gets its exclude list back,
// to be extended rather than replaced.
func codexShellPolicy(root string, worker folders) (exclude []string, legacy bool) {
	layers := []string{filepath.Join(worker.codex(), "config.toml")}
	if trusted, err := trust.FolderInEnv(worker.home, root, worker.environ); err == nil && trusted {
		layers = append(layers, filepath.Join(root, ".codex", "config.toml"))
	}
	for _, path := range layers {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var doc struct {
			Policy map[string]any `toml:"shell_environment_policy"`
		}
		if toml.Unmarshal(data, &doc) != nil {
			continue
		}
		if list, ok := doc.Policy["exclude"]; ok {
			exclude, legacy = stringList(list), true
		}
		if _, ok := doc.Policy["include_only"]; ok {
			legacy = true
		}
	}
	return exclude, legacy
}
