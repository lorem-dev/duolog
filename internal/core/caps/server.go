package caps

import "sort"

// Server is one MCP server definition from an agent's config file. It stays in
// memory: env and header values leave the adapters only as worker environment.
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

// Pairs turns a map into pairs sorted by key.
func Pairs(m map[string]string) []Pair {
	out := make([]Pair, 0, len(m))
	for k, v := range m {
		out = append(out, Pair{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
