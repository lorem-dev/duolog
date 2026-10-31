// Package capability lists the skills, plugins and MCP servers an agent has,
// without calling a model, and decides what to do when a worker lacks one.
package capability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
		items = scanClaude(root, h)
	case "codex":
		var known bool
		items, known = scanCodex(ctx, root, h)
		inv.Unknown = !known
	default:
		inv.Unknown = true
	}
	inv.Items = normalize(items)
	return inv, nil
}

func scanClaude(root, h string) []Item {
	var items []Item
	items = append(items, skillsIn(filepath.Join(h, ".claude", "skills"))...)
	items = append(items, skillsIn(filepath.Join(root, ".claude", "skills"))...)
	var installed struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	var settings struct {
		Enabled map[string]bool `json:"enabledPlugins"`
	}
	readJSON(filepath.Join(h, ".claude", "plugins", "installed_plugins.json"), &installed)
	readJSON(filepath.Join(h, ".claude", "settings.json"), &settings)
	for id, installs := range installed.Plugins {
		if on, ok := settings.Enabled[id]; ok && !on {
			continue
		}
		items = append(items, Item{Kind: KindPlugin, Name: strings.SplitN(id, "@", 2)[0]})
		for _, in := range installs {
			if in.InstallPath != "" {
				items = append(items, skillsIn(filepath.Join(in.InstallPath, "skills"))...)
			}
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
	readJSON(filepath.Join(h, ".claude.json"), &user)
	// Only the keys are used: the values hold env and headers.
	for _, set := range []map[string]json.RawMessage{project.MCP, user.MCP, user.Projects[root].MCP} {
		for name := range set {
			items = append(items, Item{Kind: KindMCP, Name: name})
		}
	}
	return items
}

func scanCodex(ctx context.Context, root, h string) ([]Item, bool) {
	var items []Item
	items = append(items, skillsIn(filepath.Join(h, ".agents", "skills"))...)
	items = append(items, skillsIn(filepath.Join(root, ".agents", "skills"))...)
	items = append(items, skillsIn(filepath.Join(root, ".codex", "skills"))...)
	config := filepath.Join(h, ".codex", "config.toml")
	data, _ := os.ReadFile(config)
	var parsed struct {
		MCP map[string]struct {
			Enabled *bool `toml:"enabled"`
		} `toml:"mcp_servers"`
	}
	if err := toml.Unmarshal(data, &parsed); err != nil {
		return items, false
	}
	for name, server := range parsed.MCP {
		if server.Enabled == nil || *server.Enabled {
			items = append(items, Item{Kind: KindMCP, Name: name})
		}
	}
	plugins, ok := cachedPlugins(ctx, root, data)
	return append(items, plugins...), ok
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
		if _, ok := find(b, it.Kind, it.Name); !ok {
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

// HasDuolog reports whether the agent's hook or plugin files mention duolog.
// Without it a worker has the sandbox only and no permission rules.
func HasDuolog(agent, root string) bool {
	h, err := home()
	if err != nil {
		return false
	}
	var files []string
	switch agent {
	case "claude":
		files = []string{filepath.Join(h, ".claude", "settings.json"), filepath.Join(root, ".claude", "settings.json"), filepath.Join(root, ".claude", "settings.local.json"), filepath.Join(h, ".claude", "plugins", "installed_plugins.json")}
	case "codex":
		files = []string{filepath.Join(h, ".codex", "hooks.json"), filepath.Join(root, ".codex", "hooks.json"), filepath.Join(h, ".codex", "config.toml")}
	}
	for _, f := range files {
		if data, err := os.ReadFile(f); err == nil && strings.Contains(string(data), "duolog") {
			return true
		}
	}
	return false
}
