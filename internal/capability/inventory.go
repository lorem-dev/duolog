// Package capability lists the skills, plugins and MCP servers an agent has,
// without calling a model, and decides what to do when a worker lacks one.
package capability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/lorem-dev/duolog/internal/store"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/install"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/trust"
	"github.com/lorem-dev/duolog/internal/trusthome"
	"github.com/pelletier/go-toml/v2"
)

const (
	KindSkill  = "skill"
	KindPlugin = "plugin"
	KindMCP    = "mcp"
)

// Item holds names only. Source is the absolute SKILL.md path of a skill and
// is empty for the other kinds. No env, header or token is ever stored.
type Item struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Source string `json:"source,omitempty"`
}

// Inventory is what one agent has. Unknown means the list could not be read
// without a model call, so nothing may be claimed about what it lacks.
type Inventory struct {
	Agent   string `json:"agent"`
	Items   []Item `json:"items"`
	Unknown bool   `json:"unknown,omitempty"`
	// UnknownKinds lists kinds whose list is known to be incomplete, for
	// example mcp for Claude (connectors and plugin-provided servers are not
	// in any file).
	UnknownKinds []string `json:"unknown_kinds,omitempty"`
}

// KindUnknown reports whether nothing may be claimed as absent for kind.
func (inv Inventory) KindUnknown(kind string) bool {
	if inv.Unknown {
		return true
	}
	for _, k := range inv.UnknownKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Home replaces the user home directory when set; tests use it.
var Home string

func home() (string, error) {
	if Home != "" {
		return Home, nil
	}
	return os.UserHomeDir()
}

// Scan reads the lists from files and from one cheap command. root is the
// project root; the command result is cached under root/.duolog/cache until a
// config file of the agent changes. A missing binary gives an unknown inventory.
func Scan(ctx context.Context, agent, root string) (Inventory, error) {
	inv := Inventory{Agent: agent, Items: []Item{}}
	if _, err := exec.LookPath(agent); err != nil {
		inv.Unknown = true
		return inv, nil
	}
	h, err := home()
	if err != nil {
		return inv, err
	}
	var items []Item
	switch agent {
	case "claude":
		items = scanClaude(ctx, root, h)
		inv.UnknownKinds = []string{KindMCP}
	case "codex":
		var unknown []string
		items, unknown = scanCodex(ctx, root, h)
		inv.UnknownKinds = unknown
		for _, it := range items {
			// Plugins may bring skills and MCP servers that no file lists.
			if it.Kind == KindPlugin {
				inv.UnknownKinds = append(inv.UnknownKinds, KindSkill, KindMCP)
				break
			}
		}
	default:
		inv.Unknown = true
	}
	inv.Items = normalize(items)
	path := filepath.Join(project.StateRoot(root), ".duolog", "cache", "inventory-"+agent+".json")
	// An uninitialized project gets no cache, so that a read-only command does not
	// create .duolog/ with no .gitignore, which hooks would then treat as set up.
	if _, statErr := os.Stat(filepath.Join(project.StateRoot(root), ".duolog", ".gitignore")); statErr != nil {
		return inv, nil
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

func scanClaude(ctx context.Context, root, h string) []Item {
	var items []Item
	items = append(items, skillsIn(filepath.Join(trusthome.Claude(h), "skills"))...)
	items = append(items, skillsIn(filepath.Join(root, ".claude", "skills"))...)
	var settings struct {
		Enabled map[string]bool `json:"enabledPlugins"`
	}
	readJSON(filepath.Join(trusthome.Claude(h), "settings.json"), &settings)
	type plugin struct {
		ID          string `json:"id"`
		Enabled     *bool  `json:"enabled"`
		InstallPath string `json:"installPath"`
	}
	var plugins []plugin
	if !claudePluginList(ctx, &plugins) {
		var installed struct {
			Plugins map[string][]struct {
				InstallPath string `json:"installPath"`
			} `json:"plugins"`
		}
		readJSON(filepath.Join(trusthome.Claude(h), "plugins", "installed_plugins.json"), &installed)
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
		items = append(items, Item{Kind: KindPlugin, Name: strings.SplitN(p.ID, "@", 2)[0]})
		if p.InstallPath != "" {
			items = append(items, skillsIn(filepath.Join(p.InstallPath, "skills"))...)
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
	readJSON(filepath.Join(root, ".mcp.json"), &project)
	readJSON(trusthome.ClaudeState(h), &user)
	// Only the keys are used: the values hold env and headers.
	for _, set := range []map[string]json.RawMessage{project.MCP, user.MCP, user.Projects[root].MCP, user.Projects[filepath.ToSlash(root)].MCP} {
		for name := range set {
			items = append(items, Item{Kind: KindMCP, Name: name})
		}
	}
	return items
}

func boolPtr(b bool) *bool { return &b }

// claudePluginList runs `claude plugin list --json`, which needs no model.
func claudePluginList(ctx context.Context, into any) bool {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "plugin", "list", "--json")
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.Output()
	return err == nil && json.Unmarshal(out, into) == nil
}

func scanCodex(ctx context.Context, root, h string) ([]Item, []string) {
	var items []Item
	items = append(items, skillsIn(filepath.Join(h, ".agents", "skills"))...)
	items = append(items, skillsIn(filepath.Join(trust.CodexHome(h), "skills"))...)
	items = append(items, skillsIn(filepath.Join(root, ".agents", "skills"))...)
	items = append(items, skillsIn(filepath.Join(root, ".codex", "skills"))...)
	config := filepath.Join(trust.CodexHome(h), "config.toml")
	data, _ := os.ReadFile(config)
	var parsed struct {
		MCP map[string]struct {
			Enabled *bool `toml:"enabled"`
		} `toml:"mcp_servers"`
	}
	var unknown []string
	if err := toml.Unmarshal(data, &parsed); err != nil {
		unknown = append(unknown, KindMCP)
	}
	for name, server := range parsed.MCP {
		if server.Enabled == nil || *server.Enabled {
			items = append(items, Item{Kind: KindMCP, Name: name})
		}
	}
	plugins, ok := cachedPlugins(ctx, root, data)
	if !ok {
		unknown = append(unknown, KindPlugin)
	}
	return append(items, plugins...), unknown
}

type cacheFile struct {
	Key   string `json:"key"`
	Items []Item `json:"items"`
}

// cachedPlugins runs `codex plugin list --json` only when the hash of the
// Codex config changed. The plain form prints 1.8 MB and is never used.
func cachedPlugins(ctx context.Context, root string, config []byte) ([]Item, bool) {
	sum := sha256.Sum256(config)
	key := hex.EncodeToString(sum[:])
	path := filepath.Join(root, ".duolog", "cache", "capabilities-codex.json")
	var cached cacheFile
	if readJSON(path, &cached) && cached.Key == key {
		return cached.Items, true
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", "plugin", "list", "--json")
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
	var items []Item
	for _, p := range list.Installed {
		if p.Enabled && p.Name != "" {
			items = append(items, Item{Kind: KindPlugin, Name: p.Name})
		}
	}
	items = normalize(items)
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

// skillsIn lists <dir>/<name>/SKILL.md entries.
func skillsIn(dir string) []Item {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var items []Item
	for _, e := range entries {
		path := filepath.Join(dir, e.Name(), "SKILL.md")
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		items = append(items, Item{Kind: KindSkill, Name: e.Name(), Source: path})
	}
	return items
}

func readJSON(path string, into any) bool {
	data, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(data, into) == nil
}

func normalize(items []Item) []Item {
	seen := make(map[[2]string]bool)
	out := []Item{}
	for _, it := range items {
		k := [2]string{it.Kind, it.Name}
		if !seen[k] {
			seen[k] = true
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Diff returns the items of a that b lacks. It is directional.
func Diff(a, b Inventory) []Item {
	out := []Item{}
	for _, it := range a.Items {
		if _, ok := find(b, it.Kind, it.Name); !ok && !b.KindUnknown(it.Kind) {
			out = append(out, it)
		}
	}
	return out
}

func find(inv Inventory, kind, name string) (Item, bool) {
	for _, it := range inv.Items {
		if it.Kind != kind {
			continue
		}
		if it.Name == name || (kind == KindSkill && strings.HasSuffix(name, ":"+it.Name)) {
			return it, true
		}
	}
	return Item{}, false
}

// HasDuolog reports whether duolog's hooks or its enabled plugin are in the
// agent's settings. Without them a worker has the sandbox only and no permission
// rules. A path that only contains the word, such as a project folder, does not count.
func HasDuolog(agent, root string) bool {
	h, err := home()
	if err != nil {
		return false
	}
	switch agent {
	case "claude":
		if install.PluginEnabled(root, h) {
			return true
		}
		for _, path := range install.ClaudeSettingsFiles(root, h) {
			if install.HasOurHook(path) {
				return true
			}
		}
	case "codex":
		for _, path := range []string{filepath.Join(trust.CodexHome(h), "hooks.json"), filepath.Join(root, ".codex", "hooks.json")} {
			if install.HasOurHook(path) {
				return true
			}
		}
		var config struct {
			Plugins map[string]struct {
				Enabled *bool `toml:"enabled"`
			} `toml:"plugins"`
		}
		data, err := os.ReadFile(filepath.Join(trust.CodexHome(h), "config.toml"))
		if err != nil || toml.Unmarshal(data, &config) != nil {
			return false
		}
		plugin, ok := config.Plugins["duolog@duolog"]
		return ok && (plugin.Enabled == nil || *plugin.Enabled)
	}
	return false
}

// Cached never invokes a program or refreshes a list on the lead hook path.
func Cached(agent, root string) Inventory {
	inv := Inventory{Agent: agent, Unknown: true}
	var cached Inventory
	if readJSON(filepath.Join(project.StateRoot(root), ".duolog", "cache", "inventory-"+agent+".json"), &cached) && cached.Agent == agent {
		return cached
	}
	return inv
}
