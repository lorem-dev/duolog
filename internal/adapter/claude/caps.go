package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/core/caps"
	"github.com/lorem-dev/duolog/internal/core/run"
	"github.com/lorem-dev/duolog/internal/install"
	"github.com/lorem-dev/duolog/internal/project"
)

// Caps lists Claude's skills, plugins and MCP servers without calling a
// model, and takes MCP servers of another agent for one Claude worker.
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

// Scan reads Claude's lists for a process whose environment is environ. Its
// MCP list is never complete: connectors and plugin-provided servers are in
// no file.
func (c Caps) Scan(ctx context.Context, root string, environ []string) (caps.Inventory, error) {
	return kit.ScanInventory("claude", project.StateRoot(root), c.Home, environ, func(f kit.Folders) ([]caps.Item, []string) {
		items := scanClaude(ctx, root, f)
		return append(items, kit.ServerItems(claudeServers(root, f))...), []string{caps.KindMCP}
	})
}

// Cached is the inventory the last scan of Claude cached for root.
func (Caps) Cached(root string) caps.Inventory {
	return kit.CachedInventory("claude", project.StateRoot(root))
}

func scanClaude(ctx context.Context, root string, f kit.Folders) []caps.Item {
	var items []caps.Item
	items = append(items, kit.SkillsIn(filepath.Join(f.Claude(), "skills"))...)
	items = append(items, kit.SkillsIn(filepath.Join(root, ".claude", "skills"))...)
	var settings struct {
		Enabled map[string]bool `json:"enabledPlugins"`
	}
	kit.ReadJSON(filepath.Join(f.Claude(), "settings.json"), &settings)
	type plugin struct {
		ID          string `json:"id"`
		Enabled     *bool  `json:"enabled"`
		InstallPath string `json:"installPath"`
	}
	var plugins []plugin
	if !claudePluginList(ctx, &plugins, f.Environ) {
		var installed struct {
			Plugins map[string][]struct {
				InstallPath string `json:"installPath"`
			} `json:"plugins"`
		}
		kit.ReadJSON(filepath.Join(f.Claude(), "plugins", "installed_plugins.json"), &installed)
		plugins = nil
		for id, installs := range installed.Plugins {
			for _, in := range installs {
				on, ok := settings.Enabled[id]
				plugins = append(plugins, plugin{ID: id, Enabled: boolPtr(!ok || on), InstallPath: in.InstallPath})
			}
		}
	}
	for _, p := range plugins {
		if p.Enabled != nil && !*p.Enabled {
			continue
		}
		items = append(items, caps.Item{Kind: caps.KindPlugin, Name: strings.SplitN(p.ID, "@", 2)[0]})
		if p.InstallPath != "" {
			items = append(items, kit.SkillsIn(filepath.Join(p.InstallPath, "skills"))...)
		}
	}
	type servers struct {
		MCP map[string]json.RawMessage `json:"mcpServers"`
	}
	var project servers
	var user struct {
		servers
		Projects map[string]servers `json:"projects"`
	}
	kit.ReadJSON(filepath.Join(root, ".mcp.json"), &project)
	kit.ReadJSON(f.ClaudeState(), &user)
	// Only the keys are used: the values hold env and headers.
	for _, set := range []map[string]json.RawMessage{project.MCP, user.MCP, user.Projects[root].MCP, user.Projects[filepath.ToSlash(root)].MCP} {
		for name := range set {
			items = append(items, caps.Item{Kind: caps.KindMCP, Name: name})
		}
	}
	return items
}

func boolPtr(b bool) *bool { return &b }

// claudePluginList runs `claude plugin list --json`, which needs no model,
// in environ, so it lists the plugins of the folders that environment
// selects.
func claudePluginList(ctx context.Context, into any, environ []string) bool {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "plugin", "list", "--json")
	cmd.Env = environ
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.Output()
	return err == nil && json.Unmarshal(out, into) == nil
}

// HasDuolog reports whether duolog's hooks or its enabled plugin are in
// Claude's settings, for a process whose environment is environ. Without them
// a worker has the sandbox only and no permission rules. A path that only
// contains the word, such as a project folder, does not count.
func (c Caps) HasDuolog(root string, environ []string) bool {
	f, err := kit.FoldersIn(c.Home, environ)
	if err != nil {
		return false
	}
	if install.PluginEnabledIn(root, f.Home, f.Environ) {
		return true
	}
	for _, path := range install.ClaudeSettingsFilesIn(root, f.Home, f.Environ) {
		if install.HasOurHook(path) {
			return true
		}
	}
	return false
}

// Servers reads the MCP servers Claude defines in its config files for the
// project at root, with Claude's own precedence, for a process whose
// environment is environ: the config files are those under its home folder and
// CLAUDE_CONFIG_DIR. An environment without a home folder gives none.
func (c Caps) Servers(root string, environ []string) map[string]caps.Server {
	f, err := kit.FoldersIn(c.Home, environ)
	if err != nil {
		return nil
	}
	return claudeServers(root, f)
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
func claudeServers(root string, f kit.Folders) map[string]caps.Server {
	var state struct {
		MCP      map[string]json.RawMessage `json:"mcpServers"`
		Projects map[string]claudeProject   `json:"projects"`
	}
	statePath := f.ClaudeState()
	kit.ReadJSON(statePath, &state)
	project := state.Projects[root]
	if local, ok := state.Projects[filepath.ToSlash(root)]; ok && project.MCP == nil {
		project = local
	}
	out := make(map[string]caps.Server)
	for name, raw := range state.MCP {
		out[name] = parseClaude(name, "user", statePath+" mcpServers", raw)
	}
	var file struct {
		MCP map[string]json.RawMessage `json:"mcpServers"`
	}
	mcpJSON := filepath.Join(root, ".mcp.json")
	kit.ReadJSON(mcpJSON, &file)
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
func claudeApproved(name, root string, f kit.Folders, project claudeProject) bool {
	approved, _ := claudeApproval(name, root, f, project)
	return approved
}

// claudeApproval is claudeApproved that also reports a settings file that
// exists but cannot be read or decoded, whose decisions are then unknown.
func claudeApproval(name, root string, f kit.Folders, project claudeProject) (bool, error) {
	var unreadable error
	all := project.EnableAllMCPServer != nil && *project.EnableAllMCPServer
	enabled := slices.Contains(project.Enabled, name)
	disabled := slices.Contains(project.Disabled, name)
	for _, path := range install.ClaudeSettingsFilesIn(root, f.Home, f.Environ) {
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
		// Only a file that is not there is missing. One that cannot be read,
		// or whose folder cannot be entered, may hold a disable.
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil || json.Unmarshal(data, &s) != nil {
			unreadable = fmt.Errorf("%s cannot be read", path)
			continue
		}
		if s.All != nil {
			all = *s.All
		}
		enabled = enabled || slices.Contains(s.Enabled, name)
		disabled = disabled || slices.Contains(s.Disabled, name)
	}
	return !disabled && (all || enabled), unreadable
}

func parseClaude(name, scope, file string, raw json.RawMessage) caps.Server {
	s := caps.Server{Name: name, Agent: "claude", Scope: scope, File: file}
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
	s.Env, s.Headers = caps.Pairs(def.Env), caps.Pairs(def.Headers)
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

// Refusal says why s cannot go to a Claude worker, from its definition alone;
// "" means it can. worker is the environment whose folders hold the worker's
// own config.
func (Caps) Refusal(root string, s caps.Server, _ []string) string {
	return refusal(s, root)
}

func refusal(s caps.Server, root string) string {
	if s.Why != "" {
		return s.Why
	}
	if s.Agent == "claude" {
		return "the worker has the same agent"
	}
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
	return ""
}

// Import translates servers of another agent into Claude's format, for one
// run. Every env and header value goes to MCP.Env under a generated name, and
// the config refers to it by that name, so no value reaches a file or argv.
// tools limits the tools of a server. worker is the environment the worker
// already gets; a name that would replace one of its values fails. A server
// that cannot be bridged is left out, with the reason in failed.
func (c Caps) Import(root string, servers map[string]caps.Server, tools map[string][]string, worker []string) (*run.MCP, map[string]string) {
	failed := map[string]string{}
	taken := kit.Taken(worker)
	out := &run.MCP{}
	claudeServers := map[string]any{}
	for _, name := range kit.SortedNames(servers) {
		s := servers[name]
		if why := refusal(s, root); why != "" {
			failed[name] = why
			continue
		}
		b := kit.NewBuilder(s, taken, c.lookup(), c.windows())
		def, err := c.build(b)
		if err != nil {
			failed[name] = err.Error()
			continue
		}
		claudeServers[name] = def
		for _, tool := range kit.Allowed(s, tools) {
			out.AllowedTools = append(out.AllowedTools, claudeTool(name, tool))
		}
		for _, tool := range s.DisabledTools {
			out.DisallowedTools = append(out.DisallowedTools, claudeTool(name, tool))
		}
		out.Env = append(out.Env, b.Commit()...)
		out.Names = append(out.Names, name)
	}
	if len(claudeServers) > 0 {
		out.ClaudeConfig, _ = json.Marshal(map[string]any{"mcpServers": claudeServers})
	}
	if len(out.Names) == 0 {
		return nil, failed
	}
	return out, failed
}

// claudeTool is the name Claude gives an MCP tool in permission rules.
func claudeTool(server, tool string) string {
	return "mcp__" + caps.ClaudeName(server) + "__" + caps.ClaudeName(tool)
}

func (c Caps) build(b *kit.Builder) (map[string]any, error) {
	s := b.Server
	lookupEnv := c.lookup()
	switch s.Transport {
	case "stdio":
		def := map[string]any{"type": "stdio", "command": s.Command, "args": nonNil(s.Args)}
		env := map[string]string{}
		for _, p := range s.Env {
			if err := kit.ReservedName(p.Key); err != nil {
				return nil, err
			}
			env[p.Key] = "${" + b.Secret(p.Value) + "}"
		}
		// env_vars name variables of the lead; one that is not set is skipped,
		// as Codex skips it. A reserved name fails even when it is not set.
		for _, name := range s.EnvVars {
			if err := kit.ReservedName(name); err != nil {
				return nil, err
			}
			if v, ok := lookupEnv(name); ok {
				env[name] = "${" + b.Secret(v) + "}"
			}
		}
		if len(env) > 0 {
			def["env"] = env
		}
		return def, nil
	case "http":
		def := map[string]any{"type": "http", "url": s.URL}
		if kit.SecretURL(s.URL) {
			def["url"] = "${" + b.Secret(s.URL) + "}"
		}
		headers := map[string]string{}
		for _, p := range s.Headers {
			headers[p.Key] = "${" + b.Secret(p.Value) + "}"
		}
		for _, p := range s.EnvHeaders {
			// Codex leaves out a header whose variable is not set.
			if v, ok := lookupEnv(p.Value); ok {
				headers[p.Key] = "${" + b.Secret(v) + "}"
			}
		}
		if s.BearerVar != "" {
			v, ok := lookupEnv(s.BearerVar)
			if !ok {
				return nil, fmt.Errorf("it reads $%s, which the lead does not have", s.BearerVar)
			}
			headers["Authorization"] = "Bearer ${" + b.Secret(v) + "}"
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

// Limit applies capabilities.mcp_tools to servers a Claude worker has itself,
// so that the list is the agent's filter as it is for a bridged server. Claude
// has no such filter, so it gets --allowedTools.
func (Caps) Limit(_ string, names []string, tools map[string][]string, _ []string) (codexArgs, allowedTools []string) {
	sorted := append([]string(nil), names...)
	slices.Sort(sorted)
	for _, name := range sorted {
		list, ok := tools[name]
		if !ok {
			continue
		}
		for _, tool := range list {
			allowedTools = append(allowedTools, claudeTool(name, tool))
		}
	}
	return nil, allowedTools
}
