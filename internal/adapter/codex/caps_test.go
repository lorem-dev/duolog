package codex

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/core/caps"
	"github.com/lorem-dev/duolog/internal/testbin"
)

func putFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0600); err != nil {
		t.Fatal(err)
	}
}

func capsFixture(t *testing.T, name string) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "scripts", "bench", "fixtures", "capabilities", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// capsEnv gives Caps with an empty home and a project root. Callers narrow
// PATH with capsPath after installing fakes, because installing needs the go
// tool.
func capsEnv(t *testing.T) (c Caps, home, root string) {
	t.Helper()
	home, root = t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return Caps{Home: home}, home, root
}

func capsPath(t *testing.T, bins ...string) {
	t.Helper()
	dirs := []string{t.TempDir()}
	for _, b := range bins {
		dirs = append(dirs, filepath.Dir(b))
	}
	t.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))
}

func itemNames(inv caps.Inventory) []string {
	var out []string
	for _, it := range inv.Items {
		out = append(out, it.Kind+":"+it.Name)
	}
	return out
}

func pluginList(t *testing.T, stdout string) string {
	t.Helper()
	return testbin.Install(t, "codex", testbin.Scenario{Responses: map[string]testbin.Scenario{"plugin list --json": {Stdout: stdout}}})
}

// The scan reads the skills under Caps.Home and never the home folder of the
// environment: a test sets the field, not a package variable.
func TestCapsHomeIsAField(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	real := t.TempDir()
	t.Setenv("HOME", real)
	t.Setenv("USERPROFILE", real)
	putFile(t, filepath.Join(real, ".agents", "skills", "from-env-home", "SKILL.md"), "x")
	dir := t.TempDir()
	putFile(t, filepath.Join(dir, ".agents", "skills", "from-field", "SKILL.md"), "x")
	capsPath(t, pluginList(t, `{"installed":[]}`))
	inv, err := Caps{Home: dir}.Scan(context.Background(), t.TempDir(), os.Environ())
	if err != nil || inv.Unknown {
		t.Fatal(inv, err)
	}
	got := strings.Join(itemNames(inv), " ")
	if !strings.Contains(got, "skill:from-field") || strings.Contains(got, "from-env-home") {
		t.Fatalf("items = %s; want the skills under Caps.Home only", got)
	}
}

func TestCodexProjectLayerNeedsTrust(t *testing.T) {
	cp, home, root := capsEnv(t)
	user := "[mcp_servers.s]\nurl = \"https://user.example.com/mcp\"\nbearer_token_env_var = \"T\"\n"
	putFile(t, filepath.Join(root, ".codex", "config.toml"), "[mcp_servers.s]\nurl = \"https://project.example.com/mcp\"\n[mcp_servers.p]\ncommand = \"p\"\n")
	putFile(t, filepath.Join(home, ".codex", "config.toml"), user)
	if got := cp.Servers(root, os.Environ()); got["s"].URL != "https://user.example.com/mcp" || len(got) != 1 {
		t.Fatalf("untrusted project layer read: %+v", got)
	}
	abs, _ := filepath.Abs(root)
	putFile(t, filepath.Join(home, ".codex", "config.toml"), user+"[projects."+kit.TOMLString(abs)+"]\ntrust_level = \"trusted\"\n")
	got := cp.Servers(root, os.Environ())
	// The project replaces url and keeps the user's bearer variable.
	if got["s"].URL != "https://project.example.com/mcp" || got["s"].BearerVar != "T" || got["s"].Scope != "project" || got["p"].Command != "p" {
		t.Fatalf("%+v", got)
	}
}

// Codex splits a -c key at each dot with no quoting, so a quoted name would
// make a new server with no transport, and Codex would refuse its config. The
// overrides name each enabled server bare and leave out a name that a bare key
// cannot hold, a disabled server, and a server only Claude has.
func TestCodexOffNamesServersBare(t *testing.T) {
	cp, home, root := capsEnv(t)
	putFile(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"claude-only":{"command":"x"}}}`)
	putFile(t, filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.codegraph]\ncommand = 'cg'\n[mcp_servers.private]\ncommand = 'p'\n[mcp_servers.parked]\ncommand = 'x'\nenabled = false\n[mcp_servers.\"a.b\"]\ncommand = 'd'\n")
	got := strings.Join(cp.Off(root, os.Environ()), "|")
	want := "-c|mcp_servers.codegraph.enabled=false|-c|mcp_servers.private.enabled=false"
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// The same class as re-review Minor 2: the servers a skill test turns off
// are those the worker's own CODEX_HOME defines, not duolog's.
func TestCodexOffEnvReadsTheWorkersConfig(t *testing.T) {
	_, home, root := capsEnv(t)
	putFile(t, filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.codegraph]\ncommand = 'cg'\n")
	worker := t.TempDir()
	putFile(t, filepath.Join(worker, "config.toml"), "[mcp_servers.tracker]\ncommand = 't'\n")
	got := strings.Join(Caps{}.Off(root, []string{"HOME=" + home, "USERPROFILE=" + home, "CODEX_HOME=" + worker}), "|")
	if want := "-c|mcp_servers.tracker.enabled=false"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestScanMissingAgentIsEmpty(t *testing.T) {
	cp, _, root := capsEnv(t)
	capsPath(t)
	inv, err := cp.Scan(context.Background(), root, os.Environ())
	if err != nil || len(inv.Items) != 0 || !inv.Unknown || inv.Agent != "codex" {
		t.Fatal(inv, err)
	}
}

func TestCacheInvalidatedByConfigChange(t *testing.T) {
	cp, home, root := capsEnv(t)
	if err := os.MkdirAll(filepath.Join(root, ".duolog"), 0o755); err != nil {
		t.Fatal(err)
	}
	codex := pluginList(t, capsFixture(t, "codex-plugin-list.json"))
	capsPath(t, codex)
	config := filepath.Join(home, ".codex", "config.toml")
	putFile(t, config, "model = \"a\"\n")
	scan := func() caps.Inventory {
		t.Helper()
		inv, err := cp.Scan(context.Background(), root, os.Environ())
		if err != nil || inv.Unknown {
			t.Fatal(inv, err)
		}
		return inv
	}
	ran := func() bool {
		_, err := os.Stat(codex + ".args.json")
		os.Remove(codex + ".args.json")
		return err == nil
	}
	first := scan()
	if !ran() {
		t.Fatal("first scan did not run the command")
	}
	second := scan()
	if ran() || strings.Join(itemNames(first), " ") != strings.Join(itemNames(second), " ") {
		t.Fatal("unchanged config ran the command again or changed the result")
	}
	putFile(t, config, "model = \"b\"\n")
	scan()
	if !ran() {
		t.Fatal("changed config did not scan again")
	}
}

func TestScanCodexWithPluginsLeavesSkillAndMCPUnknown(t *testing.T) {
	cp, _, root := capsEnv(t)
	capsPath(t, pluginList(t, capsFixture(t, "codex-plugin-list.json")))
	inv, err := cp.Scan(context.Background(), root, os.Environ())
	if err != nil || inv.Unknown || !inv.KindUnknown(caps.KindSkill) || !inv.KindUnknown(caps.KindMCP) || inv.KindUnknown(caps.KindPlugin) {
		t.Fatal(inv, err)
	}
}

func TestCodexPluginFailureKeepsOtherKindsKnown(t *testing.T) {
	cp, home, root := capsEnv(t)
	capsPath(t, pluginList(t, "not json"))
	putFile(t, filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.codegraph]\ncommand = \"x\"\n")
	inv, err := cp.Scan(context.Background(), root, os.Environ())
	if err != nil || inv.Unknown || !inv.KindUnknown(caps.KindPlugin) || inv.KindUnknown(caps.KindMCP) {
		t.Fatal(inv, err)
	}
	if got := strings.Join(itemNames(inv), " "); got != "mcp:codegraph" {
		t.Fatalf("items = %s, want mcp:codegraph", got)
	}
}
