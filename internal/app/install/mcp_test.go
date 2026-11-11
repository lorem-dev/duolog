package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// mcpEntries are the allow entries install adds, as Claude names the tools.
var mcpEntries = []string{"mcp__duolog__delegate", "mcp__duolog__peer", "mcp__duolog__status"}

// userState is a Claude user state file with keys install must keep.
const userState = "{\n  \"numStartups\": 1e3,\n  \"projects\": {\"/work/app\": {\"allowedTools\": []}}\n}\n"

// serverFile is Claude's user state file for a test whose home is home:
// setup clears CLAUDE_CONFIG_DIR.
func serverFile(home string) string { return filepath.Join(home, ".claude.json") }

// hasServer reports whether the state file under home lists duolog's server.
func hasServer(t *testing.T, home string) bool {
	t.Helper()
	data, err := os.ReadFile(serverFile(home))
	if os.IsNotExist(err) {
		return false
	}
	var state struct {
		MCP map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err != nil || json.Unmarshal(data, &state) != nil {
		t.Fatalf("%s: %v", data, err)
	}
	s, ok := state.MCP["duolog"]
	return ok && s.Command == "duolog" && slices.Equal(s.Args, []string{"mcp"})
}

// allowed returns permissions.allow of the settings file at path.
func allowed(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	var s struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err != nil || json.Unmarshal(data, &s) != nil {
		t.Fatalf("%s: %v", data, err)
	}
	return s.Permissions.Allow
}

// hasAll reports whether every one of want is in have.
func hasAll(have, want []string) bool {
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}

func TestRunProjectScopeAddsServerAndProjectAllow(t *testing.T) {
	home, root := setup(t)
	write(t, serverFile(home), userState)
	report := run(t, Options{Scope: "project", Agent: "claude", Home: home, Root: root})
	if !hasServer(t, home) {
		t.Fatal("no server entry")
	}
	settings := filepath.Join(root, ".claude", "settings.json")
	got := allowed(t, settings)
	if !hasAll(got, mcpEntries) || slices.Contains(got, "mcp__duolog__answer_ask") || slices.Contains(got, "mcp__duolog") {
		t.Fatalf("allow %q", got)
	}
	if user := allowed(t, filepath.Join(home, ".claude", "settings.json")); len(user) != 0 {
		t.Fatalf("user allow %q", user)
	}
	if !slices.Contains(report.Files, serverFile(home)) || !slices.Contains(report.Files, settings) {
		t.Fatalf("files %q", report.Files)
	}
	if !strings.HasPrefix(read(t, serverFile(home)), "{\n  \"numStartups\": 1e3,\n  \"projects\": {\"/work/app\": {\"allowedTools\": []}}") {
		t.Fatalf("other keys changed:\n%s", read(t, serverFile(home)))
	}
	// No backup next to the state file: it is large and changes often.
	if _, err := os.Stat(serverFile(home) + backupSuffix); !os.IsNotExist(err) {
		t.Fatalf("backup written: %v", err)
	}
}

func TestRunUserScopeAddsServerAndUserAllow(t *testing.T) {
	home, root := setup(t)
	run(t, Options{Scope: "user", Agent: "claude", Home: home, Root: root})
	if !hasServer(t, home) {
		t.Fatal("no server entry")
	}
	if got := allowed(t, filepath.Join(home, ".claude", "settings.json")); !hasAll(got, mcpEntries) {
		t.Fatalf("user allow %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("project settings written: %v", err)
	}
}

func TestRunWithPluginEnabledStillAddsServerAndAllow(t *testing.T) {
	home, root := setup(t)
	settings := filepath.Join(root, ".claude", "settings.json")
	write(t, settings, `{"enabledPlugins": {"duolog@duolog": true}}`)
	report := run(t, Options{Scope: "project", Agent: "claude", Home: home, Root: root})
	if !hasServer(t, home) || !hasAll(allowed(t, settings), mcpEntries) {
		t.Fatalf("server %v, allow %q", hasServer(t, home), allowed(t, settings))
	}
	if strings.Contains(read(t, settings), "\"hooks\"") {
		t.Fatalf("hooks written beside the plugin:\n%s", read(t, settings))
	}
	if !slices.Contains(report.Files, settings) {
		t.Fatalf("files %q", report.Files)
	}
	current(t, check(t, Options{Scope: "project", Agent: "claude", Home: home, Root: root})["claude"])
}

func TestRunTwiceChangesNothing(t *testing.T) {
	home, root := setup(t)
	write(t, serverFile(home), userState)
	for _, scope := range []string{"project", "user"} {
		o := Options{Scope: scope, Agent: "both", Home: home, Root: root}
		run(t, o)
		if again := run(t, o); len(again.Files) != 0 {
			t.Fatalf("%s: second run wrote %q", scope, again.Files)
		}
	}
}

func TestRemoveTakesOutExactlyWhatRunAdded(t *testing.T) {
	home, root := setup(t)
	write(t, serverFile(home), userState)
	settings := filepath.Join(root, ".claude", "settings.json")
	write(t, settings, "{\n  \"model\": \"x\",\n  \"permissions\": {\n    \"allow\": [\n      \"Bash(make:*)\"\n    ]\n  }\n}\n")
	if runtime.GOOS != "windows" {
		if err := os.Chmod(serverFile(home), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before := map[string]string{serverFile(home): read(t, serverFile(home)), settings: read(t, settings)}
	o := Options{Scope: "project", Agent: "claude", Home: home, Root: root}
	run(t, o)
	if info, err := os.Stat(serverFile(home)); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, err %v", info.Mode(), err)
	}
	report, err := testDeps().Uninstall(o)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range before {
		if got := read(t, path); got != want {
			t.Errorf("%s:\n%s\nwant\n%s", path, got, want)
		}
	}
	note := "duolog MCP server removed for all projects; other duolog projects get it back with duolog install"
	if !slices.Contains(report.Notes, note) {
		t.Fatalf("notes %q", report.Notes)
	}
	// An entry the person changed stays, with a note.
	theirs := `{"mcpServers": {"duolog": {"command": "duolog-fork", "args": ["mcp"]}}}`
	write(t, serverFile(home), theirs)
	report, err = testDeps().Uninstall(o)
	if err != nil || read(t, serverFile(home)) != theirs {
		t.Fatalf("err %v, file %s", err, read(t, serverFile(home)))
	}
	if len(report.Notes) != 1 || !strings.Contains(report.Notes[0], "stays") {
		t.Fatalf("notes %q", report.Notes)
	}
}

// Uninstall removes only the allow entries install added: one the person had
// before stays, at either scope, and so does one install found already there
// on a second run.
func TestRemoveKeepsAllowEntriesThePersonHad(t *testing.T) {
	home, root := setup(t)
	for _, scope := range []string{"project", "user"} {
		settings := filepath.Join(root, ".claude", "settings.json")
		if scope == "user" {
			settings = filepath.Join(home, ".claude", "settings.json")
		}
		before := "{\"permissions\":{\"allow\":[\"mcp__duolog__status\"]}}"
		write(t, settings, before)
		o := Options{Scope: scope, Agent: "claude", Home: home, Root: root}
		run(t, o)
		if !hasAll(allowed(t, settings), mcpEntries) {
			t.Fatalf("%s: allow %q", scope, allowed(t, settings))
		}
		run(t, o) // a second run finds every entry there and keeps its record
		if _, err := testDeps().Uninstall(o); err != nil {
			t.Fatal(err)
		}
		if got := allowed(t, settings); !slices.Equal(got, []string{"mcp__duolog__status"}) {
			t.Fatalf("%s: allow after uninstall %q", scope, got)
		}
		if _, err := os.Stat(allowRecord(testDeps().Agents[0], o, map[string]string{"project": root, "user": home}[scope])); !os.IsNotExist(err) {
			t.Fatalf("%s: the record of added entries stayed: %v", scope, err)
		}
	}
	// Without a record, uninstall takes out no allow entry: it cannot tell
	// them from the person's.
	settings := filepath.Join(root, ".claude", "settings.json")
	write(t, settings, `{"permissions":{"allow":["mcp__duolog__peer"]}}`)
	if _, err := testDeps().Uninstall(Options{Scope: "project", Agent: "claude", Home: home, Root: root}); err != nil {
		t.Fatal(err)
	}
	if got := allowed(t, settings); !slices.Equal(got, []string{"mcp__duolog__peer"}) {
		t.Fatalf("allow %q", got)
	}
}

func TestRemoveIsIdempotent(t *testing.T) {
	home, root := setup(t)
	write(t, serverFile(home), userState)
	o := Options{Scope: "project", Agent: "both", Home: home, Root: root}
	run(t, o)
	if err := Remove(o); err != nil {
		t.Fatal(err)
	}
	after, homeAfter := snapshot(t, root), snapshot(t, home)
	report, err := testDeps().Uninstall(o)
	if err != nil {
		t.Fatal(err)
	}
	if !equal(after, snapshot(t, root)) || !equal(homeAfter, snapshot(t, home)) {
		t.Fatal("a second uninstall changed files")
	}
	if len(report.Notes) != 0 || len(report.Files) != 0 {
		t.Fatalf("second uninstall reported %+v", report)
	}
	if hasServer(t, home) {
		t.Fatal("server left")
	}
}

func TestCheckReportsMissingServer(t *testing.T) {
	o, _ := installed(t)
	current(t, check(t, o)["claude"])
	write(t, serverFile(o.Home), userState)
	s := check(t, o)["claude"]
	if !s.Stale() || !slices.Contains(s.Changed, serverFile(o.Home)) {
		t.Fatalf("status %+v", s)
	}
	// duolog's entry in another form counts too.
	write(t, serverFile(o.Home), `{"mcpServers": {"duolog": {"command": "duolog", "args": ["mcp"]}}}`)
	if s := check(t, o)["claude"]; !slices.Contains(s.Changed, serverFile(o.Home)) {
		t.Fatalf("status %+v", s)
	}
	if s := check(t, o)["codex"]; s.Stale() {
		t.Fatalf("codex %+v", s)
	}
}

// An mcpServers.duolog that runs something else is the person's: install
// leaves it with a note, Check names it without calling the install stale,
// and uninstall leaves it too.
func TestRunLeavesPersonsServerEntry(t *testing.T) {
	home, root := setup(t)
	theirs := "{\n  \"mcpServers\": {\"duolog\": {\"command\": \"duolog-fork\", \"args\": [\"mcp\"]}}\n}\n"
	write(t, serverFile(home), theirs)
	o := Options{Scope: "project", Agent: "claude", Home: home, Root: root}
	report := run(t, o)
	if read(t, serverFile(home)) != theirs {
		t.Fatalf("install changed the entry:\n%s", read(t, serverFile(home)))
	}
	note := serverFile(home) + ": mcpServers.duolog runs something else; rename it"
	if !slices.Contains(report.Notes, note) {
		t.Fatalf("notes %q", report.Notes)
	}
	s := check(t, o)["claude"]
	if s.Stale() || s.ForeignServer != serverFile(home) {
		t.Fatalf("status %+v", s)
	}
	line := "claude: mcpServers.duolog in " + serverFile(home) + " runs something else, so duolog leaves it and the lead has no duolog tools; rename it and run duolog install"
	if lines := s.Lines(root); !slices.Contains(lines, line) {
		t.Fatalf("lines %q", lines)
	}
	if lines := testDeps().StaleLines(root, home); !slices.Contains(lines, line) {
		t.Fatalf("doctor lines %q", lines)
	}
	if _, err := testDeps().Uninstall(o); err != nil || read(t, serverFile(home)) != theirs {
		t.Fatalf("err %v, file %s", err, read(t, serverFile(home)))
	}
}

func TestCheckReportsMissingAllow(t *testing.T) {
	o, root := installed(t)
	settings := filepath.Join(root, ".claude", "settings.json")
	data := read(t, settings)
	write(t, settings, strings.Replace(data, `"mcp__duolog__peer",`, "", 1))
	if read(t, settings) == data {
		t.Fatalf("fixture: no peer entry in\n%s", data)
	}
	s := check(t, o)["claude"]
	if !s.Stale() || !slices.Equal(rel(t, root, s.Changed), []string{".claude/settings.json"}) {
		t.Fatalf("status %+v", s)
	}
	// A settings file with only the allow entries is an install, though no
	// hook or skill is left.
	home, root := setup(t)
	write(t, filepath.Join(root, ".claude", "settings.json"), `{"permissions": {"allow": ["mcp__duolog__status"]}}`)
	s = check(t, Options{Scope: "project", Agent: "claude", Home: home, Root: root})["claude"]
	if !s.Installed || !s.Stale() {
		t.Fatalf("status %+v", s)
	}
}

// codexTable is duolog's MCP server table in Codex's config.toml, as the
// Codex installer without configured names writes it: env_vars forwards
// what duolog needs from the lead past Codex's short server environment.
const codexTable = "[mcp_servers.duolog]\ncommand = \"duolog\"\nargs = [\"mcp\"]\ntool_timeout_sec = 660\n" +
	`env_vars = ["APPDATA", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "DUOLOG_DEPTH", "DUOLOG_PEER", "DUOLOG_TELEMETRY", "LOCALAPPDATA", "REX_BIN_DIR", "REX_BLOCK", "REX_SESSION", "SYSTEMROOT", "USERPROFILE", "XDG_CACHE_HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME"]` + "\n"

// A Codex install adds duolog's server table to config.toml under
// CODEX_HOME, keeping the person's settings, and writes no allow list into
// hooks.json; uninstall takes the table out again.
func TestInstallCodexMCP(t *testing.T) {
	for _, scope := range []string{"project", "user"} {
		t.Run(scope, func(t *testing.T) {
			home, root := setup(t)
			codexHome := filepath.Join(home, "codex-home")
			t.Setenv("CODEX_HOME", codexHome)
			config := filepath.Join(codexHome, "config.toml")
			theirs := "model = \"x\"\n[tui]\nnotifications = true\n"
			write(t, config, theirs)
			o := Options{Scope: scope, Agent: "codex", Home: home, Root: root}
			report := run(t, o)
			if got := read(t, config); got != theirs+"\n"+codexTable {
				t.Fatalf("config.toml:\n%s", got)
			}
			if !slices.Contains(report.Files, config) || !slices.Contains(report.Notes, RestartNote) {
				t.Fatalf("report %+v", report)
			}
			// The person's file is kept as it was before duolog's first change,
			// as AdoptWorktree keeps it.
			if got := read(t, config+".duolog.bak"); got != theirs {
				t.Fatalf("backup:\n%s", got)
			}
			if _, err := os.Stat(serverFile(home)); !os.IsNotExist(err) {
				t.Fatalf("codex install wrote %s: %v", serverFile(home), err)
			}
			hooks := testDeps().Agents[1].Installer.HookFile(scope, map[string]string{"project": root, "user": home}[scope], home)
			if got := read(t, hooks); strings.Contains(got, "permissions") || strings.Contains(got, "mcp__duolog") {
				t.Fatalf("%s:\n%s", hooks, got)
			}
			current(t, check(t, o)["codex"])
			if again := run(t, o); len(again.Files) != 0 || slices.Contains(again.Notes, RestartNote) {
				t.Fatalf("second run %+v", again)
			}
			// The person's table under duolog's name stays, and Check names it.
			uninstall, err := testDeps().Uninstall(o)
			if err != nil {
				t.Fatal(err)
			}
			if got := read(t, config); got != theirs {
				t.Fatalf("after uninstall:\n%s", got)
			}
			if !slices.Contains(uninstall.Notes, ServerRemovedNote) {
				t.Fatalf("notes %q", uninstall.Notes)
			}
		})
	}
}

// An mcp_servers.duolog of Codex that runs something else is the person's:
// install leaves it with a note and Check names it. An inline table of
// duolog's own is refused with nothing changed.
func TestInstallCodexMCPLeavesPersonsEntry(t *testing.T) {
	home, root := setup(t)
	codexHome := filepath.Join(home, "codex-home")
	t.Setenv("CODEX_HOME", codexHome)
	config := filepath.Join(codexHome, "config.toml")
	theirs := "[mcp_servers.duolog]\ncommand = \"other\"\n"
	write(t, config, theirs)
	o := Options{Scope: "project", Agent: "codex", Home: home, Root: root}
	report := run(t, o)
	if read(t, config) != theirs || !slices.Contains(report.Notes, config+": mcp_servers.duolog runs something else; rename it") {
		t.Fatalf("notes %q, config:\n%s", report.Notes, read(t, config))
	}
	if s := check(t, o)["codex"]; s.Stale() || s.ForeignServer != config {
		t.Fatalf("status %+v", s)
	}
	inline := "mcp_servers.duolog = { command = \"duolog\", args = [\"mcp\"] }\n"
	freshHome, freshRoot := setup(t)
	t.Setenv("CODEX_HOME", filepath.Join(freshHome, "codex-home"))
	config = filepath.Join(freshHome, "codex-home", "config.toml")
	write(t, config, inline)
	beforeRoot, beforeHome := snapshot(t, freshRoot), snapshot(t, freshHome)
	o = Options{Scope: "project", Agent: "codex", Home: freshHome, Root: freshRoot}
	if _, err := Run(o); err == nil || !strings.Contains(err.Error(), "mcp_servers.duolog is not a [table]; nothing changed") {
		t.Fatalf("err %v", err)
	}
	// Nothing at all is written: not the hooks, the skills or the config.
	if !equal(beforeRoot, snapshot(t, freshRoot)) || !equal(beforeHome, snapshot(t, freshHome)) {
		t.Fatalf("a refused install wrote files: root %v, home %v", snapshot(t, freshRoot), snapshot(t, freshHome))
	}
}

// A config.toml go-toml cannot read is the person's to fix: install, of
// both agents, goes on without duolog's table and says so, and so does
// uninstall.
func TestInstallSkipsUnreadableCodexConfig(t *testing.T) {
	home, root := setup(t)
	codexHome := filepath.Join(home, "codex-home")
	t.Setenv("CODEX_HOME", codexHome)
	config := filepath.Join(codexHome, "config.toml")
	broken := "model = \n"
	write(t, config, broken)
	o := Options{Scope: "project", Agent: "both", Home: home, Root: root}
	report, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	if read(t, config) != broken || !hasServer(t, home) {
		t.Fatalf("config.toml %q, Claude's server %v", read(t, config), hasServer(t, home))
	}
	note := func(notes []string) bool {
		return slices.ContainsFunc(notes, func(n string) bool {
			return strings.HasPrefix(n, config+": ") && strings.Contains(n, "duolog's MCP server is not in it")
		})
	}
	if !note(report.Notes) {
		t.Fatalf("notes %q", report.Notes)
	}
	// install --check and doctor say so too, as a note: no error, and not a
	// stale install.
	s := check(t, o)["codex"]
	line := "codex: " + config + " does not parse, so duolog's MCP server is not in it and the lead has no duolog tools; fix the file and run duolog install"
	if s.Err != nil || s.Stale() || s.UnreadableServer != config || !slices.Contains(s.Lines(root), line) {
		t.Fatalf("status %+v, lines %q", s, s.Lines(root))
	}
	if lines := testDeps().StaleLines(root, home); !slices.Contains(lines, line) {
		t.Fatalf("doctor lines %q", lines)
	}
	report, err = testDeps().Uninstall(o)
	if err != nil || read(t, config) != broken || !note(report.Notes) {
		t.Fatalf("uninstall: %v, %q, notes %q", err, read(t, config), report.Notes)
	}
}

func TestStaleLinesSayDeferredLoadingOff(t *testing.T) {
	o, root := installed(t)
	line := "duolog MCP: deferred tool loading is off (ENABLE_TOOL_SEARCH=false), so its four tools load in full every turn; turn it on or run duolog uninstall to drop the server"
	t.Setenv("ENABLE_TOOL_SEARCH", "")
	if lines := testDeps().StaleLines(root, o.Home); slices.Contains(lines, line) {
		t.Fatalf("lines %q", lines)
	}
	t.Setenv("ENABLE_TOOL_SEARCH", "false")
	if lines := testDeps().StaleLines(root, o.Home); !slices.Contains(lines, line) {
		t.Fatalf("lines %q", lines)
	}
	// Without the server the setting costs duolog nothing.
	if err := Remove(o); err != nil {
		t.Fatal(err)
	}
	if lines := testDeps().StaleLines(root, o.Home); slices.Contains(lines, line) {
		t.Fatalf("lines %q", lines)
	}
}
