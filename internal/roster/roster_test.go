package roster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/capability"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/session"
)

func setup(t *testing.T) (*config.Config, *session.Registry) {
	t.Helper()
	home := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(key, home)
	}
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	return cfg, reg
}
func TestRosterLineUnderBudget(t *testing.T) {
	cfg, reg := setup(t)
	line := Line(cfg, "lead", reg)
	if float64(len(strings.Fields(line)))*1.5 > 80 {
		t.Fatal(line)
	}
	for _, want := range []string{"plan=opus", "tests=terra", "mechanical=luna"} {
		if !strings.Contains(line, want) {
			t.Fatalf("%q missing %q", line, want)
		}
	}
	if strings.Contains(line, "gpt-") || strings.Contains(line, "claude-") {
		t.Fatal(line)
	}
}
func TestRosterMarksUnavailable(t *testing.T) {
	cfg, reg := setup(t)
	if err := reg.MarkUnavailable("codex", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if line := Line(cfg, "", reg); !strings.Contains(line, "codex=unavailable") {
		t.Fatal(line)
	}
}
func TestRosterDoesNotRunAgents(t *testing.T) {
	cfg, reg := setup(t)
	if Line(cfg, "", reg) == "" {
		t.Fatal("empty roster")
	}
	if _, err := os.Stat(reg.Dir); err != nil {
		t.Fatal(err)
	}
}

func TestRosterCountsDifferences(t *testing.T) {
	cfg, reg := setup(t)
	lead := capability.Inventory{Agent: "claude", Items: []capability.Item{{Kind: "skill", Name: "a"}, {Kind: "skill", Name: "b"}, {Kind: "mcp", Name: "c"}, {Kind: "mcp", Name: "shared"}}}
	other := capability.Inventory{Agent: "codex", Items: []capability.Item{{Kind: "mcp", Name: "shared"}, {Kind: "plugin", Name: "only-codex"}}}
	note := CapsNote(lead, other, true)
	if note != "codex lacks 3" {
		t.Fatal(note)
	}
	full := Line(cfg, "lead", reg) + "\n" + note
	if float64(len(strings.Fields(full)))*1.5 > 80 {
		t.Fatal(full)
	}
	if CapsNote(lead, capability.Inventory{Agent: "codex", Unknown: true}, true) != "" {
		t.Fatal("unknown inventory reported as a difference")
	}
}
func TestRosterWarnsWhenDuologMissing(t *testing.T) {
	inv := capability.Inventory{Agent: "claude"}
	note := CapsNote(inv, capability.Inventory{Agent: "codex"}, false)
	if !strings.Contains(note, "codex has no duolog hooks") || !strings.Contains(note, "sandbox only") {
		t.Fatal(note)
	}
}

func TestStartNoteUsesOnlyCachedCapabilities(t *testing.T) {
	_, reg := setup(t)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	root := t.TempDir()
	cache := filepath.Join(root, ".duolog", "cache")
	if err := os.MkdirAll(cache, 0755); err != nil {
		t.Fatal(err)
	}
	mine := capability.Inventory{Agent: "claude", Items: []capability.Item{{Kind: "skill", Name: "extra"}}}
	other := capability.Inventory{Agent: "codex", Items: []capability.Item{}}
	for _, inv := range []capability.Inventory{mine, other} {
		data, _ := json.Marshal(inv)
		if err := os.WriteFile(filepath.Join(cache, "inventory-"+inv.Agent+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// setup removed every executable from PATH; cached notes still work.
	note := StartNote(root, session.Lead{ID: "lead", Target: config.Target{Agent: "claude"}})
	if !strings.Contains(note, "caps=1") || !strings.Contains(note, "codex has no duolog hooks") {
		t.Fatal(note)
	}
	line := Line(&config.Config{Roles: map[string]config.Role{"work": {Use: config.Target{Agent: "claude", Model: "sonnet", Effort: "low"}}}}, "lead", reg)
	if !strings.Contains(line, "sonnet/low") {
		t.Fatal(line)
	}
}
