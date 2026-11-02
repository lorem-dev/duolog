package capability

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/lorem-dev/duolog/internal/runner"
)

// ActionBridge hands a server of the lead's agent to the worker for one run.
const ActionBridge = "bridge"

// lookupEnv reads the lead's environment; tests replace it.
var lookupEnv = os.LookupEnv

// Bridgeable lists the servers of agent that a worker of the other agent could
// borrow, and why the others cannot be. It reads config files only.
func Bridgeable(agent, root string) (ok []string, not map[string]string) {
	not = map[string]string{}
	other := "codex"
	if agent == "codex" {
		other = "claude"
	}
	for name, s := range Servers(agent, root) {
		if why := staticWhy(s, other, root); why != "" {
			not[name] = why
		} else {
			ok = append(ok, name)
		}
	}
	sort.Strings(ok)
	return ok, not
}

var codexName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// staticWhy says why s cannot go to agent to, from its definition alone.
func staticWhy(s Server, to, root string) string {
	if s.Why != "" {
		return s.Why
	}
	if s.Agent == to {
		return "the worker has the same agent"
	}
	switch to {
	case "codex":
		if !codexName.MatchString(s.Name) {
			return "its name has characters that a Codex -c key cannot hold"
		}
		for _, p := range s.Env {
			if !envName.MatchString(p.Key) {
				return "an env name is not a plain variable name"
			}
		}
		if s.Transport == "stdio" {
			for _, arg := range append([]string{s.Command}, s.Args...) {
				if claudeRef.MatchString(arg) {
					// Codex takes args as text, so the value would land in argv.
					return "its command or args read a variable, which Codex cannot do"
				}
			}
		}
	case "claude":
		if s.Transport == "stdio" && s.Cwd != "" {
			cwd := s.Cwd
			if !filepath.IsAbs(cwd) {
				cwd = filepath.Join(root, cwd)
			}
			if filepath.Clean(cwd) != filepath.Clean(root) {
				return "it sets a working folder, which Claude cannot do"
			}
		}
		for _, text := range append([]string{s.Command, s.URL}, s.Args...) {
			if strings.Contains(text, "${") {
				return "its command, args or url contain ${, which Claude would expand"
			}
		}
	}
	return ""
}

// claudeRef matches ${VAR} and ${VAR:-default}, the two forms Claude expands.
var claudeRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}`)

// expand resolves Claude references from the lead's environment. An unset
// variable with no default fails: Claude would send the text ${VAR} instead.
func expand(text string) (string, error) {
	var missing string
	out := claudeRef.ReplaceAllStringFunc(text, func(ref string) string {
		m := claudeRef.FindStringSubmatch(ref)
		if v, ok := lookupEnv(m[1]); ok {
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

// Bridge translates the named servers of agent from into the format of agent to,
// for one run. Every env and header value goes to MCP.Env under a generated
// name, and the config refers to it by that name, so no value reaches a file
// or argv. tools limits the tools of a server. workerEnv is the environment
// the worker already gets; a name that would replace one of its values fails.
// A server that cannot be bridged is left out, with the reason in failed.
func Bridge(from, to, root string, names []string, tools map[string][]string, workerEnv []string) (*runner.MCP, map[string]string) {
	failed := map[string]string{}
	servers := Servers(from, root)
	taken := map[string]string{}
	for _, kv := range workerEnv {
		k, v, _ := strings.Cut(kv, "=")
		taken[k] = v
	}
	out := &runner.MCP{}
	claudeServers := map[string]any{}
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	for _, name := range sorted {
		s, ok := servers[name]
		if !ok {
			failed[name] = "the lead's config does not define it"
			continue
		}
		if why := staticWhy(s, to, root); why != "" {
			failed[name] = why
			continue
		}
		b := &builder{server: s, taken: taken, env: map[string]string{}}
		var err error
		switch to {
		case "codex":
			var args []string
			args, err = b.codex(allowed(s, tools))
			if err == nil {
				out.CodexArgs = append(out.CodexArgs, args...)
			}
		case "claude":
			var def map[string]any
			def, err = b.claude()
			if err == nil {
				claudeServers[name] = def
				for _, tool := range allowed(s, tools) {
					out.AllowedTools = append(out.AllowedTools, claudeTool(name, tool))
				}
				for _, tool := range s.DisabledTools {
					out.DisallowedTools = append(out.DisallowedTools, claudeTool(name, tool))
				}
			}
		default:
			err = fmt.Errorf("agent %s is unknown", to)
		}
		if err != nil {
			failed[name] = err.Error()
			continue
		}
		for _, k := range b.order {
			taken[k] = b.env[k]
			out.Env = append(out.Env, k+"="+b.env[k])
		}
		out.Names = append(out.Names, name)
	}
	if len(claudeServers) > 0 {
		out.ClaudeConfig, _ = json.Marshal(map[string]any{"mcpServers": claudeServers})
	}
	if to == "codex" && len(out.Names) > 0 {
		// Codex shell commands inherit the worker's environment by default, so
		// the bridged values are kept from the model's shell.
		out.CodexArgs = append(out.CodexArgs, "-c", `shell_environment_policy.filters.DUOLOG_MCP_*="exclude"`)
		for _, kv := range out.Env {
			if k, _, _ := strings.Cut(kv, "="); !strings.HasPrefix(k, "DUOLOG_MCP_") {
				out.CodexArgs = append(out.CodexArgs, "-c", "shell_environment_policy.filters."+k+`="exclude"`)
			}
		}
	}
	if len(out.Names) == 0 {
		return nil, failed
	}
	return out, failed
}

// allowed is the duolog list for the server, else the lead's own Codex list.
func allowed(s Server, tools map[string][]string) []string {
	if list, ok := tools[s.Name]; ok {
		return list
	}
	return s.EnabledTools
}

var claudeNameChars = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// claudeTool is the name Claude gives an MCP tool in permission rules.
func claudeTool(server, tool string) string {
	return "mcp__" + claudeNameChars.ReplaceAllString(server, "_") + "__" + claudeNameChars.ReplaceAllString(tool, "_")
}

type builder struct {
	server Server
	taken  map[string]string
	env    map[string]string
	order  []string
	n      int
}

// secret stores value under a new DUOLOG_MCP_<SERVER>_<N> name and returns it.
func (b *builder) secret(value string) string {
	base := "DUOLOG_MCP_" + strings.ToUpper(regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(b.server.Name, "_"))
	for {
		b.n++
		name := fmt.Sprintf("%s_%d", base, b.n)
		if _, used := b.taken[name]; used {
			continue
		}
		if _, used := b.env[name]; used {
			continue
		}
		b.set(name, value)
		return name
	}
}

func (b *builder) set(name, value string) {
	b.env[name] = value
	b.order = append(b.order, name)
}

// keep passes a stdio variable under its own name, which the server reads. A
// name the worker already has with another value, or one of duolog's, fails.
func (b *builder) keep(name, value string) error {
	if strings.HasPrefix(name, "DUOLOG_") || strings.HasPrefix(name, "REX_") {
		return fmt.Errorf("it sets $%s, a name duolog keeps for itself", name)
	}
	if old, ok := b.taken[name]; ok && old != value {
		return fmt.Errorf("it sets $%s, which the worker already has with another value", name)
	}
	if _, ok := b.env[name]; !ok {
		b.set(name, value)
	}
	return nil
}

// value resolves one config value as the lead's agent would read it.
func (b *builder) value(v string) (string, error) {
	if b.server.Agent == "claude" {
		return expand(v)
	}
	return v, nil
}

func (b *builder) codex(tools []string) ([]string, error) {
	s := b.server
	key := "mcp_servers." + s.Name + "."
	var args []string
	add := func(field, value string) { args = append(args, "-c", key+field+"="+value) }
	switch s.Transport {
	case "stdio":
		add("command", tomlString(s.Command))
		if len(s.Args) > 0 {
			add("args", tomlArray(s.Args))
		}
		if s.Cwd != "" {
			add("cwd", tomlString(s.Cwd))
		}
		// Codex gives a stdio server a short default environment plus env and
		// env_vars. env would put the value in argv, so every variable passes
		// through env_vars from the worker's environment, under its own name.
		var names []string
		for _, p := range s.Env {
			v, err := b.value(p.Value)
			if err != nil {
				return nil, err
			}
			if err := b.keep(p.Key, v); err != nil {
				return nil, err
			}
			names = append(names, p.Key)
		}
		if len(names) > 0 {
			add("env_vars", tomlArray(names))
		}
	case "http":
		u, err := b.value(s.URL)
		if err != nil {
			return nil, err
		}
		if secretURL(u) {
			return nil, fmt.Errorf("its url has a query or user info, which Codex would get in argv")
		}
		add("url", tomlString(u))
		headers := map[string]string{}
		for _, p := range s.Headers {
			v, err := b.value(p.Value)
			if err != nil {
				return nil, err
			}
			headers[p.Key] = b.secret(v)
		}
		if len(headers) > 0 {
			add("env_http_headers", tomlTable(headers))
		}
	default:
		return nil, fmt.Errorf("its transport %s is unknown", s.Transport)
	}
	if tools != nil {
		add("enabled_tools", tomlArray(tools))
	}
	return args, nil
}

func (b *builder) claude() (map[string]any, error) {
	s := b.server
	switch s.Transport {
	case "stdio":
		def := map[string]any{"type": "stdio", "command": s.Command, "args": nonNil(s.Args)}
		env := map[string]string{}
		for _, p := range s.Env {
			env[p.Key] = "${" + b.secret(p.Value) + "}"
		}
		// env_vars name variables of the lead; one that is not set is skipped,
		// as Codex skips it.
		for _, name := range s.EnvVars {
			if v, ok := lookupEnv(name); ok {
				env[name] = "${" + b.secret(v) + "}"
			}
		}
		if len(env) > 0 {
			def["env"] = env
		}
		return def, nil
	case "http":
		def := map[string]any{"type": "http", "url": s.URL}
		if secretURL(s.URL) {
			def["url"] = "${" + b.secret(s.URL) + "}"
		}
		headers := map[string]string{}
		for _, p := range s.Headers {
			headers[p.Key] = "${" + b.secret(p.Value) + "}"
		}
		for _, p := range s.EnvHeaders {
			// Codex leaves out a header whose variable is not set.
			if v, ok := lookupEnv(p.Value); ok {
				headers[p.Key] = "${" + b.secret(v) + "}"
			}
		}
		if s.BearerVar != "" {
			v, ok := lookupEnv(s.BearerVar)
			if !ok {
				return nil, fmt.Errorf("it reads $%s, which the lead does not have", s.BearerVar)
			}
			headers["Authorization"] = "Bearer ${" + b.secret(v) + "}"
		}
		if len(headers) > 0 {
			def["headers"] = headers
		}
		return def, nil
	}
	return nil, fmt.Errorf("its transport %s is unknown", s.Transport)
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

// secretURL reports a url that may carry a credential in itself.
func secretURL(raw string) bool {
	u, err := url.Parse(raw)
	return err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != ""
}

// tomlString writes a TOML basic string. TOML has no \x escape, so control
// characters use \u.
func tomlString(s string) string {
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

func tomlArray(list []string) string {
	parts := make([]string, len(list))
	for i, s := range list {
		parts[i] = tomlString(s)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func tomlTable(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = tomlString(k) + "=" + tomlString(m[k])
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// Request returns the url and headers that the agent sends to an HTTP server,
// with every reference read from the lead's environment. It is for a check of
// the credentials only; the result must not be logged or stored.
func (s Server) Request() (string, map[string]string, error) {
	if s.Transport != "http" {
		return "", nil, fmt.Errorf("it is not an HTTP server")
	}
	b := &builder{server: s}
	u, err := b.value(s.URL)
	if err != nil {
		return "", nil, err
	}
	headers := map[string]string{}
	for _, p := range s.Headers {
		v, err := b.value(p.Value)
		if err != nil {
			return "", nil, err
		}
		headers[p.Key] = v
	}
	for _, p := range s.EnvHeaders {
		if v, ok := lookupEnv(p.Value); ok {
			headers[p.Key] = v
		}
	}
	if s.BearerVar != "" {
		v, ok := lookupEnv(s.BearerVar)
		if !ok {
			return "", nil, fmt.Errorf("it reads $%s, which the lead does not have", s.BearerVar)
		}
		headers["Authorization"] = "Bearer " + v
	}
	return u, headers, nil
}
