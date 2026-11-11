package plugin

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The Claude plugin ships the pane mod: its hooks file names the module, and
// the module is embedded where ModFile says.
func TestClaudePluginShipsTheMod(t *testing.T) {
	var doc struct {
		Modules []string `json:"modules"`
	}
	data, err := FS.ReadFile("claude/hooks/hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(doc.Modules, []string{"./duolog-pane.tsx"}) {
		t.Fatalf("modules = %q, want [./duolog-pane.tsx]", doc.Modules)
	}
	src, err := FS.ReadFile(ModFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "export const register") {
		t.Fatalf("%s has no register export", ModFile)
	}
	if ModTestedWith == "" {
		t.Fatal("ModTestedWith is empty")
	}
}

// The plugin ships the mod's state contract at types/index.d.ts, where
// Claude Code finds it by itself. The manifest does not name it: Claude Code
// 2.0 and early 2.1 refuse a manifest with a key they do not know, and that
// would stop the whole plugin, its gate, hooks and skills.
func TestClaudePluginShipsTheModContract(t *testing.T) {
	if _, err := os.Stat(filepath.Join("claude", "types", "index.d.ts")); err != nil {
		t.Fatal(err)
	}
	var manifest map[string]json.RawMessage
	data, err := os.ReadFile(filepath.Join("claude", ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if _, has := manifest["types"]; has {
		t.Fatal(`plugin.json has a "types" key, which older Claude Code refuses`)
	}
}

// The binary embeds what duolog reads, not the mod's tests, its contract or
// the API types a Claude Code session writes beside a plugin it loads.
func TestEmbedLeavesOutDevFiles(t *testing.T) {
	for _, path := range []string{"claude/test", "claude/types", "claude/.claude-plugin/types", "claude/.claude-plugin"} {
		if _, err := fs.Stat(FS, path); err == nil {
			t.Errorf("FS holds %s", path)
		}
	}
	for _, path := range []string{"claude/hooks/hooks.json", ModFile, "claude/skills/duolog/SKILL.md", "codex/skills/duolog/SKILL.md", "codex/plugin.json"} {
		if _, err := fs.Stat(FS, path); err != nil {
			t.Errorf("FS lacks %s: %v", path, err)
		}
	}
}
