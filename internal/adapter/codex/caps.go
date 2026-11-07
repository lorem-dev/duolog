package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/core/caps"
	"github.com/lorem-dev/duolog/internal/core/run"
	"github.com/lorem-dev/duolog/internal/install"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/trust"
	"github.com/pelletier/go-toml/v2"
)

// Caps lists Codex's skills, plugins and MCP servers without calling a
// model, and takes MCP servers of another agent for one Codex worker.
type Caps struct {
	// Home replaces the user home folder when set; tests set it.
	Home string
	// LookupEnv reads the lead's environment; nil is os.LookupEnv.
	LookupEnv func(string) (string, bool)
	// GOOS is the system whose variable names the bridge compares; "" is
	// runtime.GOOS.
	GOOS string
}

func (c Caps) lookup() func(string) (string, bool) {
	if c.LookupEnv != nil {
		return c.LookupEnv
	}
	return os.LookupEnv
}

func (c Caps) windows() bool {
	goos := c.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	return goos == "windows"
}

// Scan reads Codex's lists for a process whose environment is environ. A
// plugin may bring skills and MCP servers that no file lists.
func (c Caps) Scan(ctx context.Context, root string, environ []string) (caps.Inventory, error) {
	return kit.ScanInventory("codex", project.StateRoot(root), c.Home, environ, func(f kit.Folders) ([]caps.Item, []string) {
		items, unknown := scanCodex(ctx, root, f)
		for _, it := range items {
			// Plugins may bring skills and MCP servers that no file lists.
			if it.Kind == caps.KindPlugin {
				unknown = append(unknown, caps.KindSkill, caps.KindMCP)
				break
			}
		}
		return items, unknown
	})
}

// Cached is the inventory the last scan of Codex cached for root.
func (Caps) Cached(root string) caps.Inventory {
	return kit.CachedInventory("codex", project.StateRoot(root))
}

func scanCodex(ctx context.Context, root string, f kit.Folders) ([]caps.Item, []string) {
	var items []caps.Item
	items = append(items, kit.SkillsIn(filepath.Join(f.Home, ".agents", "skills"))...)
	items = append(items, kit.SkillsIn(filepath.Join(f.Codex(), "skills"))...)
	items = append(items, kit.SkillsIn(filepath.Join(root, ".agents", "skills"))...)
	items = append(items, kit.SkillsIn(filepath.Join(root, ".codex", "skills"))...)
	config := filepath.Join(f.Codex(), "config.toml")
	data, _ := os.ReadFile(config)
	var parsed struct {
		MCP map[string]struct {
			Enabled *bool `toml:"enabled"`
		} `toml:"mcp_servers"`
	}
	var unknown []string
	if err := toml.Unmarshal(data, &parsed); err != nil {
		unknown = append(unknown, caps.KindMCP)
	}
	for name, server := range parsed.MCP {
		if server.Enabled == nil || *server.Enabled {
			items = append(items, caps.Item{Kind: caps.KindMCP, Name: name})
		}
	}
	plugins, ok := cachedPlugins(ctx, root, data, f.Environ)
	if !ok {
		unknown = append(unknown, caps.KindPlugin)
	}
	return append(items, plugins...), unknown
}

type cacheFile struct {
	Key   string      `json:"key"`
	Items []caps.Item `json:"items"`
}

// cachedPlugins runs `codex plugin list --json` only when the hash of the
// Codex config changed. The plain form prints 1.8 MB and is never used. The
// command runs in environ, the environment whose CODEX_HOME holds config.
func cachedPlugins(ctx context.Context, root string, config []byte, environ []string) ([]caps.Item, bool) {
	sum := sha256.Sum256(config)
	key := hex.EncodeToString(sum[:])
	path := filepath.Join(root, ".duolog", "cache", "capabilities-codex.json")
	var cached cacheFile
	if kit.ReadJSON(path, &cached) && cached.Key == key {
		return cached.Items, true
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", "plugin", "list", "--json")
	cmd.Env = environ
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.Output()
	var list struct {
		Installed []struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
		} `json:"installed"`
	}
	if err != nil || json.Unmarshal(out, &list) != nil {
		return nil, false
	}
	var items []caps.Item
	for _, p := range list.Installed {
		if p.Enabled && p.Name != "" {
			items = append(items, caps.Item{Kind: caps.KindPlugin, Name: p.Name})
		}
	}
	items = caps.Normalize(items)
	// An uninitialized project keeps no cache: hooks read .duolog as "set up here".
	if info, err := os.Stat(filepath.Join(root, ".duolog")); err != nil || !info.IsDir() {
		return items, true
	}
	if data, err := json.Marshal(cacheFile{Key: key, Items: items}); err == nil {
		if os.MkdirAll(filepath.Dir(path), 0755) == nil {
			_ = os.WriteFile(path, data, 0600)
		}
	}
	return items, true
}

// HasDuolog reports whether duolog's hooks or its enabled plugin are in
// Codex's settings, for a process whose environment is environ. Without them
// a worker has the sandbox only and no permission rules. A path that only
// contains the word, such as a project folder, does not count.
func (c Caps) HasDuolog(root string, environ []string) bool {
	f, err := kit.FoldersIn(c.Home, environ)
	if err != nil {
		return false
	}
	for _, path := range []string{filepath.Join(f.Codex(), "hooks.json"), filepath.Join(root, ".codex", "hooks.json")} {
		if install.HasOurHook(path) {
			return true
		}
	}
	var config struct {
		Plugins map[string]struct {
			Enabled *bool `toml:"enabled"`
		} `toml:"plugins"`
	}
	data, err := os.ReadFile(filepath.Join(f.Codex(), "config.toml"))
	if err != nil || toml.Unmarshal(data, &config) != nil {
		return false
	}
	plugin, ok := config.Plugins["duolog@duolog"]
	return ok && (plugin.Enabled == nil || *plugin.Enabled)
}

// Servers reads the MCP servers Codex defines in its config files for the
// project at root, with Codex's own precedence, for a process whose
// environment is environ: the config files are those under its home folder and
// CODEX_HOME. A disabled server is left out, as Codex does not start it. An
// environment without a home folder gives none.
func (c Caps) Servers(root string, environ []string) map[string]caps.Server {
	f, err := kit.FoldersIn(c.Home, environ)
	if err != nil {
		return nil
	}
	return codexServers(root, f)
}

// codexServers reads config.toml in CODEX_HOME, then the project's
// .codex/config.toml when Codex trusts the folder. A project value replaces the
// user value of the same key, table by table, as Codex merges its layers.
func codexServers(root string, f kit.Folders) map[string]caps.Server {
	merged, files, user := codexMerged(root, f)
	out := make(map[string]caps.Server)
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
func codexMerged(root string, f kit.Folders) (merged map[string]any, files map[string]string, user string) {
	user = filepath.Join(f.Codex(), "config.toml")
	merged, files = map[string]any{}, map[string]string{}
	layers := []string{user}
	if trusted, err := trust.FolderInEnv(f.Home, root, f.Environ); err == nil && trusted {
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

var codexName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Off returns -c overrides that turn off every enabled MCP server in the
// Codex config for root, for a Codex worker whose environment is environ: the
// servers turned off are those its own CODEX_HOME defines. Codex reads a -c
// key as parts split at each dot, with no TOML quoting, so the name goes in
// bare: mcp_servers."x".enabled would name a new server `"x"` with no
// transport, and Codex would refuse its config. A name that a bare key cannot
// hold is left out, as the bridge leaves it out.
func (c Caps) Off(root string, environ []string) []string {
	var names []string
	for name := range c.Servers(root, environ) {
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
func codexHas(root, name string, worker kit.Folders) bool {
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

func parseCodex(name, scope, file string, t map[string]any) caps.Server {
	s := caps.Server{Name: name, Agent: "codex", Scope: scope, File: file}
	str := func(key string) string { v, _ := t[key].(string); return v }
	s.Command, s.URL, s.Cwd, s.BearerVar = str("command"), str("url"), str("cwd"), str("bearer_token_env_var")
	s.Args = stringList(t["args"])
	s.Env = caps.Pairs(stringMap(t["env"]))
	s.Headers = caps.Pairs(stringMap(t["http_headers"]))
	s.EnvHeaders = caps.Pairs(stringMap(t["env_http_headers"]))
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
func codexShellPolicy(root string, worker kit.Folders) (exclude []string, legacy bool) {
	layers := []string{filepath.Join(worker.Codex(), "config.toml")}
	if trusted, err := trust.FolderInEnv(worker.Home, root, worker.Environ); err == nil && trusted {
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

// Refusal says why s cannot go to a Codex worker, from its definition alone
// and the worker's own Codex config, the one worker, its environment, selects;
// "" means it can.
func (c Caps) Refusal(root string, s caps.Server, worker []string) string {
	f, err := kit.FoldersIn(c.Home, worker)
	return refusal(s, root, f, err)
}

// refusal is Refusal for the worker's folders; workerErr is why worker could
// not be found, such as an environment without a home folder.
func refusal(s caps.Server, root string, worker kit.Folders, workerErr error) string {
	if s.Why != "" {
		return s.Why
	}
	if s.Agent == "codex" {
		return "the worker has the same agent"
	}
	if !codexName.MatchString(s.Name) {
		return "its name has characters that a Codex -c key cannot hold"
	}
	if workerErr != nil {
		// -c keys would merge into a table that cannot be read.
		return "the worker's Codex config cannot be read: " + workerErr.Error()
	}
	if codexHas(root, s.Name, worker) {
		// -c keys would merge into that table, a disabled one too.
		return "the worker's Codex config has a server of that name"
	}
	for _, p := range s.Env {
		if !envName.MatchString(p.Key) {
			return "an env name is not a plain variable name"
		}
	}
	if s.Transport == "http" && kit.ClaudeRef.MatchString(s.URL) {
		// Codex takes the url as text, so an expanded value would land in argv.
		return "its url reads a variable, which Codex would get in argv"
	}
	if s.Transport == "stdio" {
		for _, arg := range append([]string{s.Command}, s.Args...) {
			if kit.ClaudeRef.MatchString(arg) {
				// Codex takes args as text, so the value would land in argv.
				return "its command or args read a variable, which Codex cannot do"
			}
		}
	}
	return ""
}

// Import translates servers of another agent into Codex -c overrides, for one
// run. Every env and header value goes to MCP.Env under a generated name, and
// the config refers to it by that name, so no value reaches a file or argv.
// tools limits the tools of a server. worker is the environment the worker
// already gets; a name that would replace one of its values fails, and the
// worker's own Codex config is the one that environment selects. A server
// that cannot be bridged is left out, with the reason in failed.
func (c Caps) Import(root string, servers map[string]caps.Server, tools map[string][]string, workerEnv []string) (*run.MCP, map[string]string) {
	failed := map[string]string{}
	worker, workerErr := kit.FoldersIn(c.Home, workerEnv)
	taken := kit.Taken(workerEnv)
	out := &run.MCP{}
	for _, name := range kit.SortedNames(servers) {
		s := servers[name]
		if why := refusal(s, root, worker, workerErr); why != "" {
			failed[name] = why
			continue
		}
		b := kit.NewBuilder(s, taken, c.lookup(), c.windows())
		args, err := build(b, kit.Allowed(s, tools))
		if err != nil {
			failed[name] = err.Error()
			continue
		}
		out.CodexArgs = append(out.CodexArgs, args...)
		out.Env = append(out.Env, b.Commit()...)
		out.Names = append(out.Names, name)
	}
	if len(out.Names) > 0 {
		// Codex shell commands inherit the worker's environment by default, so
		// the bridged values are kept from the model's shell.
		hidden := []string{"DUOLOG_MCP_*"}
		for _, kv := range out.Env {
			if k, _, _ := strings.Cut(kv, "="); !strings.HasPrefix(k, "DUOLOG_MCP_") {
				hidden = append(hidden, k)
			}
		}
		if exclude, legacy := codexShellPolicy(root, worker); legacy {
			out.CodexArgs = append(out.CodexArgs, "-c", "shell_environment_policy.exclude="+kit.TOMLArray(append(exclude, hidden...)))
		} else {
			for _, k := range hidden {
				out.CodexArgs = append(out.CodexArgs, "-c", "shell_environment_policy.filters."+k+`="exclude"`)
			}
		}
	}
	if len(out.Names) == 0 {
		return nil, failed
	}
	return out, failed
}

func build(b *kit.Builder, tools []string) ([]string, error) {
	s := b.Server
	key := "mcp_servers." + s.Name + "."
	var args []string
	add := func(field, value string) { args = append(args, "-c", key+field+"="+value) }
	switch s.Transport {
	case "stdio":
		add("command", kit.TOMLString(s.Command))
		if len(s.Args) > 0 {
			add("args", kit.TOMLArray(s.Args))
		}
		if s.Cwd != "" {
			add("cwd", kit.TOMLString(s.Cwd))
		}
		// Codex gives a stdio server a short default environment plus env and
		// env_vars. env would put the value in argv, so every variable passes
		// through env_vars from the worker's environment, under its own name.
		var names []string
		for _, p := range s.Env {
			v, err := b.Value(p.Value)
			if err != nil {
				return nil, err
			}
			if err := b.Keep(p.Key, v); err != nil {
				return nil, err
			}
			names = append(names, p.Key)
		}
		if len(names) > 0 {
			add("env_vars", kit.TOMLArray(names))
		}
	case "http":
		u, err := b.Value(s.URL)
		if err != nil {
			return nil, err
		}
		if kit.SecretURL(u) {
			return nil, fmt.Errorf("its url has a query or user info, which Codex would get in argv")
		}
		add("url", kit.TOMLString(u))
		headers := map[string]string{}
		for _, p := range s.Headers {
			v, err := b.Value(p.Value)
			if err != nil {
				return nil, err
			}
			headers[p.Key] = b.Secret(v)
		}
		if len(headers) > 0 {
			add("env_http_headers", kit.TOMLTable(headers))
		}
	default:
		return nil, fmt.Errorf("its transport %s is unknown", s.Transport)
	}
	if tools != nil {
		add("enabled_tools", kit.TOMLArray(tools))
	}
	return args, nil
}

// Limit applies capabilities.mcp_tools to servers a Codex worker has itself,
// so that the list is the agent's filter as it is for a bridged server: Codex
// gets enabled_tools. enabled_tools for a server Codex does not define, such
// as a plugin's, would make a table with no transport, which Codex refuses, so
// only the servers that the config of worker, the worker's environment,
// defines get one.
func (c Caps) Limit(root string, names []string, tools map[string][]string, worker []string) (codexArgs, allowedTools []string) {
	defs := c.Servers(root, worker)
	own := slices.DeleteFunc(append([]string(nil), names...), func(name string) bool { _, ok := defs[name]; return !ok })
	slices.Sort(own)
	for _, name := range own {
		list, ok := tools[name]
		if !ok {
			continue
		}
		if codexName.MatchString(name) {
			codexArgs = append(codexArgs, "-c", "mcp_servers."+name+".enabled_tools="+kit.TOMLArray(list))
		}
	}
	return codexArgs, nil
}
