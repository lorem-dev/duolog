package roster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/launch/launchtest"
	"github.com/lorem-dev/duolog/internal/core/caps"
	"github.com/lorem-dev/duolog/internal/core/config"
)

var testAgents = []string{"claude", "codex"}

// agentCaps are the registered agents' real capabilities, which read the
// temporary home the tests set.
func agentCaps() map[string]Caps {
	out := map[string]Caps{}
	for name, c := range launchtest.AllCaps() {
		out[name] = c
	}
	return out
}

func setup(t *testing.T) (*config.Config, *session.Registry) {
	t.Helper()
	home := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(key, home)
	}
	cfg, err := configfile.Load(t.TempDir())
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
	line := Line(cfg, testAgents, "lead", reg)
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
	if line := Line(cfg, testAgents, "", reg); !strings.Contains(line, "codex=unavailable") {
		t.Fatal(line)
	}
}
func TestRosterDoesNotRunAgents(t *testing.T) {
	cfg, reg := setup(t)
	if Line(cfg, testAgents, "", reg) == "" {
		t.Fatal("empty roster")
	}
	if _, err := os.Stat(reg.Dir); err != nil {
		t.Fatal(err)
	}
}

func TestRosterCountsDifferences(t *testing.T) {
	cfg, reg := setup(t)
	lead := caps.Inventory{Agent: "claude", Items: []caps.Item{{Kind: "skill", Name: "a"}, {Kind: "skill", Name: "b"}, {Kind: "mcp", Name: "c"}, {Kind: "mcp", Name: "shared"}}}
	other := caps.Inventory{Agent: "codex", Items: []caps.Item{{Kind: "mcp", Name: "shared"}, {Kind: "plugin", Name: "only-codex"}}}
	note := CapsNote(lead, other, true)
	if note != "codex lacks 3" {
		t.Fatal(note)
	}
	full := Line(cfg, testAgents, "lead", reg) + "\n" + note
	if float64(len(strings.Fields(full)))*1.5 > 80 {
		t.Fatal(full)
	}
	if CapsNote(lead, caps.Inventory{Agent: "codex", Unknown: true}, true) != "" {
		t.Fatal("unknown inventory reported as a difference")
	}
}
func TestRosterWarnsWhenDuologMissing(t *testing.T) {
	inv := caps.Inventory{Agent: "claude"}
	note := CapsNote(inv, caps.Inventory{Agent: "codex"}, false)
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
	mine := caps.Inventory{Agent: "claude", Items: []caps.Item{{Kind: "skill", Name: "extra"}}}
	other := caps.Inventory{Agent: "codex", Items: []caps.Item{}}
	for _, inv := range []caps.Inventory{mine, other} {
		data, _ := json.Marshal(inv)
		if err := os.WriteFile(filepath.Join(cache, "inventory-"+inv.Agent+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// setup removed every executable from PATH; cached notes still work.
	note := StartNote(root, testAgents, session.Lead{ID: "lead", Target: config.Target{Agent: "claude"}}, agentCaps())
	if !strings.Contains(note, "caps=1") || !strings.Contains(note, "codex has no duolog hooks") {
		t.Fatal(note)
	}
	line := Line(&config.Config{Roles: map[string]config.Role{"work": {Use: config.Target{Agent: "claude", Model: "sonnet", Effort: "low"}}}}, testAgents, "lead", reg)
	if !strings.Contains(line, "sonnet/low") {
		t.Fatal(line)
	}
}

func TestRosterShowsAPinnedModel(t *testing.T) {
	cfg, reg := setup(t)
	role := cfg.Roles["plan"]
	role.Use.Model = "claude-opus-5-5"
	cfg.Roles["plan"] = role
	if line := Line(cfg, testAgents, "lead", reg); !strings.Contains(line, "plan=claude-opus-5-5/") {
		t.Fatal(line)
	}
}

func TestStartNoteCoauthors(t *testing.T) {
	setup(t)
	root := t.TempDir()
	dir := filepath.Join(root, ".duolog")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if note := StartNote(root, testAgents, session.Lead{}, agentCaps()); !strings.HasSuffix(note, "; commits: add duolog coauthors") {
		t.Fatal(note)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("coauthors:\n  enabled: false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if note := StartNote(root, testAgents, session.Lead{}, agentCaps()); strings.Contains(note, "commits:") {
		t.Fatal(note)
	}
}

func TestLearningNote(t *testing.T) {
	cfg := &config.Config{}
	if got := LearningNote(cfg); got != "" {
		t.Fatal(got)
	}
	cfg.Learning.Enabled = true
	if got := LearningNote(cfg); got != "; learning mode on: run duolog task start before a task" {
		t.Fatal(got)
	}
}

func TestLearningNoteNeverMentionsCalibration(t *testing.T) {
	cfg := &config.Config{}
	cfg.Learning.Enabled = true
	if got := LearningNote(cfg); strings.Contains(strings.ToLower(got), "calibrat") {
		t.Fatal(got)
	}
}
