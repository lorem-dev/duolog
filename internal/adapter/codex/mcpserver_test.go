package codex

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/adapter/mcp"
	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/run"
	"github.com/pelletier/go-toml/v2"
)

// parseTOML reads data as a TOML document.
func parseTOML(t *testing.T, data []byte) map[string]any {
	t.Helper()
	m := map[string]any{}
	if err := toml.Unmarshal(data, &m); err != nil {
		t.Fatalf("%v:\n%s", err, data)
	}
	return m
}

// ourTable is duolog's table as Installer{} writes it.
func ourTable() string { return Installer{}.table() }

// The tool timeout covers the longest wait a tool call may ask for, with a
// minute to spare.
func TestCodexServerTimeoutCoversLongestWait(t *testing.T) {
	want := "tool_timeout_sec = 660\n"
	if mcp.MaxWaitMinutes*60+60 != 660 || !strings.HasSuffix(mcpHead, want) || !strings.HasPrefix(ourTable(), mcpHead) {
		t.Fatalf("MaxWaitMinutes %d, table %q", mcp.MaxWaitMinutes, ourTable())
	}
}

// The table forwards, under their own names, the variables duolog needs
// from the lead: Codex gives a stdio server only a short default
// environment. The configured names come in through WithEnvVars.
func TestCodexServerEnvVars(t *testing.T) {
	got := Installer{}.WithEnvVars([]string{"FOO_TOKEN", "CODEX_HOME", "OPENAI_API_KEY"}).table()
	doc := parseTOML(t, []byte(got))
	entry := doc["mcp_servers"].(map[string]any)["duolog"].(map[string]any)
	var names []string
	for _, v := range entry["env_vars"].([]any) {
		names = append(names, v.(string))
	}
	for _, want := range []string{"DUOLOG_DEPTH", "DUOLOG_PEER", "CODEX_HOME", "CLAUDE_CONFIG_DIR", "REX_SESSION", "XDG_CONFIG_HOME", "FOO_TOKEN", "OPENAI_API_KEY"} {
		if !slices.Contains(names, want) {
			t.Errorf("env_vars %q lacks %s", names, want)
		}
	}
	if !slices.IsSorted(names) || len(slices.Compact(slices.Clone(names))) != len(names) {
		t.Fatalf("env_vars %q not sorted and unique", names)
	}
	if !strings.HasPrefix(got, mcpHead) || !strings.HasSuffix(got, "\n") {
		t.Fatalf("table %q", got)
	}
}

func TestCodexServerFile(t *testing.T) {
	home := t.TempDir()
	if got, want := (Installer{}).ServerFile(home, []string{"HOME=/elsewhere"}), filepath.Join(home, ".codex", "config.toml"); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	codexHome := t.TempDir()
	if got, want := (Installer{}).ServerFile(home, []string{"CODEX_HOME=" + codexHome}), filepath.Join(codexHome, "config.toml"); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// Add writes duolog's table at the end, after a blank line, and changes no
// byte before it.
func TestCodexServerAdd(t *testing.T) {
	table := ourTable()
	data, note, err := Installer{}.Server(nil, true)
	if err != nil || note != "" || string(data) != table {
		t.Fatalf("nil: %q, %q, %v", data, note, err)
	}
	old := "model = \"x\"\n[projects.\"/a\"]\ntrust_level = \"trusted\"\n"
	data, note, err = Installer{}.Server([]byte(old), true)
	if err != nil || note != "" || string(data) != old+"\n"+table {
		t.Fatalf("got %q, %q, %v", data, note, err)
	}
	want := parseTOML(t, []byte(old))
	want["mcp_servers"] = map[string]any{"duolog": parseTOML(t, []byte(table))["mcp_servers"].(map[string]any)["duolog"]}
	if got := parseTOML(t, data); !reflect.DeepEqual(got, want) {
		t.Fatalf("parsed %v, want %v", got, want)
	}
	// A second add changes nothing.
	again, note, err := Installer{}.Server(data, true)
	if err != nil || note != "" || string(again) != string(data) {
		t.Fatalf("again: %q, %q, %v", again, note, err)
	}
	// A file without a final line end keeps that style, and CRLF files stay
	// CRLF.
	data, _, err = Installer{}.Server([]byte("model = \"x\""), true)
	if err != nil || string(data) != "model = \"x\"\n\n"+strings.TrimSuffix(table, "\n") {
		t.Fatalf("no line end: %q, %v", data, err)
	}
	data, _, err = Installer{}.Server([]byte("model = \"x\"\r\n"), true)
	if err != nil || string(data) != "model = \"x\"\r\n\r\n"+strings.ReplaceAll(table, "\n", "\r\n") {
		t.Fatalf("CRLF: %q, %v", data, err)
	}
	// Other servers stay, and duolog's table joins them.
	old = "[mcp_servers.docs]\ncommand = \"docs\"\n"
	if data, _, err = (Installer{}).Server([]byte(old), true); err != nil || string(data) != old+"\n"+table {
		t.Fatalf("other server: %q, %v", data, err)
	}
}

// A duolog entry that runs something else is the person's: add and remove
// both leave it and say so.
func TestCodexServerForeign(t *testing.T) {
	old := []byte("[mcp_servers.duolog]\ncommand = \"other\"\n")
	data, note, err := Installer{}.Server(old, true)
	if err != nil || string(data) != string(old) || note != "mcp_servers.duolog runs something else; rename it" {
		t.Fatalf("add: %q, %q, %v", data, note, err)
	}
	data, note, err = Installer{}.Server(old, false)
	if err != nil || string(data) != string(old) || note != "mcp_servers.duolog does not run duolog mcp, so it stays" {
		t.Fatalf("remove: %q, %q, %v", data, note, err)
	}
	// In any shape, even one install would refuse to edit.
	inline := []byte("mcp_servers.duolog = { command = \"other\" }\n")
	if data, note, err = (Installer{}).Server(inline, true); err != nil || string(data) != string(inline) || note == "" {
		t.Fatalf("inline: %q, %q, %v", data, note, err)
	}
}

// Add rewrites duolog's own table in place when it differs, and remove takes
// it out with its sub-tables; every other byte stays.
func TestCodexServerReplaceAndRemove(t *testing.T) {
	table := ourTable()
	head := "model = \"x\"\n"
	tail := "\n# the person's own\n[tui]\nnotifications = true # keep\n"
	old := head + "\n" + mcpHead + tail
	old = strings.Replace(old, "tool_timeout_sec = 660", "tool_timeout_sec = 60", 1)
	data, note, err := Installer{}.Server([]byte(old), true)
	if err != nil || note != "" || string(data) != head+"\n"+table+tail {
		t.Fatalf("replace: %q, %q, %v", data, note, err)
	}
	data, note, err = Installer{}.Server(data, false)
	if err != nil || note != "" || string(data) != head+tail {
		t.Fatalf("remove: %q, %q, %v", data, note, err)
	}
	// Sub-tables of duolog's entry go with it, and nothing else.
	withSub := head + "\n" + table + "\n[mcp_servers.duolog.env]\nX = \"1\"\n\n[mcp_servers.docs]\ncommand = \"docs\"\n" + tail
	data, _, err = Installer{}.Server([]byte(withSub), false)
	if err != nil || string(data) != head+"\n[mcp_servers.docs]\ncommand = \"docs\"\n"+tail {
		t.Fatalf("remove with sub-table: %q, %v", data, err)
	}
	// A current entry with settings of the person's stays as it is.
	if data, note, err = (Installer{}).Server([]byte(withSub), true); err != nil || note != "" || string(data) != withSub {
		t.Fatalf("current with sub-table: %q, %q, %v", data, note, err)
	}
	// A rewrite replaces only the lines of duolog's keys: the person's
	// other keys, comments and sub-tables stay.
	persons := head + "\n[mcp_servers.duolog]\ncommand = \"duolog\"\n# off for now\nenabled = false\nargs = [\"mcp\"]\ntool_timeout_sec = 60\n\n[mcp_servers.duolog.tools.delegate]\napproval_mode = \"approve\"\n" + tail
	data, note, err = Installer{}.Server([]byte(persons), true)
	if want := head + "\n" + table + "# off for now\nenabled = false\n\n[mcp_servers.duolog.tools.delegate]\napproval_mode = \"approve\"\n" + tail; err != nil || note != "" || string(data) != want {
		t.Fatalf("replace with the person's keys: %q, %q, %v", data, note, err)
	}
	// Names the person added to env_vars stay: an entry that forwards every
	// name duolog needs is current, and a rewrite adds the missing ones.
	extra := strings.Replace(table, "env_vars = [", "env_vars = [\"MY_TOKEN\", ", 1)
	if data, note, err = (Installer{}).Server([]byte(extra), true); err != nil || note != "" || string(data) != extra {
		t.Fatalf("extra env name: %q, %q, %v", data, note, err)
	}
	more := Installer{}.WithEnvVars([]string{"ZZ_NEW"})
	data, _, err = more.Server([]byte(extra), true)
	if err != nil || !strings.Contains(string(data), "env_vars = [\"MY_TOKEN\", ") || !strings.Contains(string(data), "\"ZZ_NEW\"") {
		t.Fatalf("env union: %q, %v", data, err)
	}
	// A multi-line value of duolog's key is rewritten whole.
	multi := head + "\n[mcp_servers.duolog]\ncommand = \"duolog\"\nargs = [\n  \"mcp\",\n]\n" + tail
	if data, _, err = (Installer{}).Server([]byte(multi), true); err != nil || string(data) != head+"\n"+table+tail {
		t.Fatalf("multi-line args: %q, %v", data, err)
	}
	// Remove of a file without the entry returns it as it is.
	if data, note, err = (Installer{}).Server([]byte(head), false); err != nil || note != "" || string(data) != head {
		t.Fatalf("remove absent: %q, %q, %v", data, note, err)
	}
	if data, note, err = (Installer{}).Server(nil, false); err != nil || note != "" || data != nil {
		t.Fatalf("remove nil: %q, %q, %v", data, note, err)
	}
	// duolog's entry in a shape other than its own [table] is refused.
	for _, shape := range []string{
		"[mcp_servers]\nduolog = { command = \"duolog\", args = [\"mcp\"] }\n",
		"mcp_servers.duolog.command = \"duolog\"\nmcp_servers.duolog.args = [\"mcp\"]\n",
		"[mcp_servers]\nduolog.command = \"duolog\"\nduolog.args = [\"mcp\"]\n",
	} {
		for _, add := range []bool{true, false} {
			data, _, err := Installer{}.Server([]byte(shape), add)
			if err == nil || err.Error() != "mcp_servers.duolog is not a [table]; nothing changed" || data != nil {
				t.Errorf("%q add %v: %q, %v", shape, add, data, err)
			}
		}
	}
	// A mcp_servers that is not a table is refused too.
	if data, _, err := (Installer{}).Server([]byte("mcp_servers = 3\n"), true); err == nil || data != nil || !strings.Contains(err.Error(), "nothing changed") {
		t.Errorf("mcp_servers = 3: %q, %v", data, err)
	}
}

// Add then remove gives back the file byte for byte: no doubled blank line,
// and a file without a final line end stays without one.
func TestCodexServerRoundTrip(t *testing.T) {
	for _, before := range []string{
		"", "model = \"x\"\n", "model = \"x\"", "model = \"x\"\r\n", "model = \"x\"\r\n\r\n",
		"model = \"x\"\n\n", "# only a comment\n", "# a comment without a line end",
		"model = \"x\"\n\n# the person's own\n[tui]\nnotifications = true # keep\n",
		"[mcp_servers]\n[mcp_servers.docs]\ncommand = \"d\"\n",
	} {
		added, _, err := Installer{}.Server([]byte(before), true)
		if err != nil {
			t.Fatalf("%q: %v", before, err)
		}
		removed, _, err := Installer{}.Server(added, false)
		if err != nil || string(removed) != before {
			t.Errorf("%q: round trip %q, %v", before, removed, err)
		}
	}
	// A table the person wrote first, or with a comment above it, leaves no
	// doubled blank line; the comment stays.
	table := ourTable()
	for before, want := range map[string]string{
		table + "\n[tui]\nx = 1\n":                          "[tui]\nx = 1\n",
		"a = 1\n\n# servers\n" + table + "\n[tui]\nx = 1\n": "a = 1\n\n# servers\n\n[tui]\nx = 1\n",
		"a = 1\n" + table + "\n[tui]\nx = 1\n":              "a = 1\n\n[tui]\nx = 1\n",
	} {
		got, _, err := Installer{}.Server([]byte(before), false)
		if err != nil || string(got) != want {
			t.Errorf("%q: got %q, %v; want %q", before, got, err, want)
		}
	}
}

// A header line inside a multi-line string or array is no table: add and
// remove both see only the real ones.
func TestCodexServerSkipsHeadersInMultiLineValues(t *testing.T) {
	table := ourTable()
	for _, fake := range []string{
		"note = \"\"\"\n[mcp_servers.duolog]\ncommand = \"x\"\n\"\"\"\n",
		"note = '''\n[mcp_servers.duolog]\n'''\n",
		"nested = [\n[1],\n[2]\n]\n",
	} {
		added, _, err := Installer{}.Server([]byte(fake), true)
		if err != nil || string(added) != fake+"\n"+table {
			t.Fatalf("%q add: %q, %v", fake, added, err)
		}
		removed, _, err := Installer{}.Server(added, false)
		if err != nil || string(removed) != fake {
			t.Fatalf("%q remove: %q, %v", fake, removed, err)
		}
	}
	// A string inside duolog's own table that holds a header goes with it.
	inside := table + "note = \"\"\"\n[tui]\n\"\"\"\n"
	if removed, _, err := (Installer{}).Server([]byte(inside), false); err != nil || string(removed) != "" {
		t.Fatalf("inside: %q, %v", removed, err)
	}
}

// A config.toml that go-toml cannot read is not duolog's to fix: Server says
// so with ErrServerFileUnreadable, and install skips the table.
func TestCodexServerUnreadable(t *testing.T) {
	for _, add := range []bool{true, false} {
		data, _, err := Installer{}.Server([]byte("model = \n"), add)
		if !errors.Is(err, agent.ErrServerFileUnreadable) || data != nil {
			t.Fatalf("add %v: %q, %v", add, data, err)
		}
	}
}

// WriteServer keeps a backup of the old file, writes under the lock adopt
// uses, and refuses a file that changed since it was read.
func TestCodexWriteServer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	old := []byte("model = \"x\"\n")
	putFile(t, path, string(old))
	data, _, err := Installer{}.Server(old, true)
	if err != nil {
		t.Fatal(err)
	}
	if wrote, err := (Installer{}).WriteServer(path, old, data); err != nil || !wrote {
		t.Fatalf("wrote %v, %v", wrote, err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(data) {
		t.Fatalf("file %q", got)
	}
	if got, _ := os.ReadFile(path + configBackupSuffix); string(got) != string(old) {
		t.Fatalf("backup %q", got)
	}
	// Windows reports a writable file as 0666 whatever was asked.
	if info, err := os.Stat(path); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, %v", info, err)
	}
	// The same bytes write nothing.
	if wrote, err := (Installer{}).WriteServer(path, data, data); err != nil || wrote {
		t.Fatalf("same: %v, %v", wrote, err)
	}
	// A file that changed since it was read is left alone.
	if _, err := (Installer{}).WriteServer(path, old, []byte("x = 1\n")); err == nil || !strings.Contains(err.Error(), "changed during install") {
		t.Fatalf("changed: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(data) {
		t.Fatalf("file %q", got)
	}
	// A new file gets no backup.
	fresh := filepath.Join(dir, "home", "config.toml")
	if wrote, err := (Installer{}).WriteServer(fresh, nil, []byte(ourTable())); err != nil || !wrote {
		t.Fatalf("new: %v, %v", wrote, err)
	}
	if _, err := os.Stat(fresh + configBackupSuffix); !os.IsNotExist(err) {
		t.Fatalf("backup of a new file: %v", err)
	}
	if _, err := (Installer{}).WriteServer(fresh, nil, []byte("x = 1\n")); err == nil {
		t.Fatal("a file made meanwhile was replaced")
	}
}

// Codex has no allow list: Allow leaves the file as it is.
func TestCodexAllowChangesNothing(t *testing.T) {
	for _, old := range [][]byte{nil, []byte(`{"hooks":{}}`)} {
		for _, add := range []bool{true, false} {
			got, err := Installer{}.Allow(old, []string{"mcp__duolog__status"}, add)
			if err != nil || string(got) != string(old) {
				t.Fatalf("%q add %v: %q, %v", old, add, got, err)
			}
		}
	}
	if !(Installer{}).ServesMCP() {
		t.Fatal("ServesMCP is false")
	}
}

// codexServerEnv is the environment Codex 0.167.0 gives a stdio MCP server,
// as seen live on macOS: its default names taken from its own environment,
// then the env_vars names of the server's table.
func codexServerEnv(t *testing.T, parent []string, table string) []string {
	t.Helper()
	names := []string{"HOME", "LANG", "LC_ALL", "LOGNAME", "PATH", "SHELL", "TERM", "TMPDIR", "TZ", "USER", "__CF_USER_TEXT_ENCODING"}
	entry := parseTOML(t, []byte(table))["mcp_servers"].(map[string]any)["duolog"].(map[string]any)
	list, _ := entry["env_vars"].([]any)
	for _, v := range list {
		names = append(names, v.(string))
	}
	var env []string
	for _, pair := range parent {
		key, _, _ := strings.Cut(pair, "=")
		if slices.Contains(names, key) {
			env = append(env, pair)
		}
	}
	return env
}

// A worker or peer that starts duolog's server anyway gets no tools: the
// server sees DUOLOG_DEPTH and DUOLOG_PEER through env_vars.
func TestCodexServerSeesWorkerMarkers(t *testing.T) {
	root := t.TempDir()
	putFile(t, filepath.Join(root, ".duolog", "config.yaml"), "version: 1\n")
	base := []string{"HOME=" + t.TempDir(), "PATH=/usr/bin"}
	if on, _ := mcp.Enabled(codexServerEnv(t, base, ourTable()), root); !on {
		t.Fatal("the lead's server is off")
	}
	for _, marker := range []string{"DUOLOG_DEPTH=1", "DUOLOG_PEER=peer-1"} {
		env := codexServerEnv(t, append(slices.Clone(base), marker), ourTable())
		if on, why := mcp.Enabled(env, root); on {
			t.Errorf("%s: tools on (%s); env %q", marker, why, env)
		}
	}
}

// testSpec is a Codex worker's run in root whose process environment is env.
func testSpec(t *testing.T, root string, env []string) run.Spec {
	t.Helper()
	p := corelaunch.Process{Agent: "codex", Env: env, Prefix: []string{"codex"}, Launch: []string{"exec"}}
	r := route.Route{Target: config.Target{Agent: "codex", Model: "gpt-6.1-sol", Effort: "high"}}
	return run.Spec{Route: r, Mode: mode.Edits, Cwd: root, TaskDir: t.TempDir(), Process: &p}
}

// A Codex worker or peer whose config defines duolog's server starts with
// it turned off; one whose config does not gets no override, which would
// make a server with no transport.
func TestCodexWorkerAndPeerTurnServerOff(t *testing.T) {
	_, home, root := capsEnv(t)
	worker := t.TempDir()
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "CODEX_HOME=" + worker}
	off := "mcp_servers.duolog.enabled=false"
	argv := func() (workerArgv, peerArgv []string) {
		s := testSpec(t, root, env)
		return Worker{}.Argv(s), Pane{}.Argv(s.Route, s.Mode, *s.Process, "")
	}
	w, p := argv()
	if slices.Contains(w, off) || slices.Contains(p, off) {
		t.Fatalf("no server, yet off: %q / %q", w, p)
	}
	putFile(t, filepath.Join(worker, "config.toml"), "model = \"x\"\n\n"+ourTable())
	w, p = argv()
	for name, args := range map[string][]string{"worker": w, "peer": p} {
		i := slices.Index(args, off)
		if i < 1 || args[i-1] != "-c" {
			t.Errorf("%s argv %q lacks -c %s", name, args, off)
		}
	}
	// The skill test overrides turn it off as well.
	if got := (Caps{}).Off(root, env); !slices.Contains(got, off) {
		t.Fatalf("off %q", got)
	}
}

// offNames are the servers argv turns off with -c mcp_servers.<name>.enabled=false.
func offNames(argv []string) []string {
	var names []string
	for i, a := range argv {
		if name, ok := strings.CutSuffix(strings.TrimPrefix(a, "mcp_servers."), ".enabled=false"); ok && i > 0 && argv[i-1] == "-c" && strings.HasPrefix(a, "mcp_servers.") {
			names = append(names, name)
		}
	}
	return names
}

// Every server that runs duolog mcp is turned off, under any table name and
// whatever path names the binary; a server named otherwise that runs
// something else is not.
func TestCodexServerOffByCommand(t *testing.T) {
	_, home, root := capsEnv(t)
	worker := t.TempDir()
	putFile(t, filepath.Join(worker, "config.toml"), "[mcp_servers.lead_tools]\ncommand = \"/usr/local/bin/duolog\"\nargs = [\"mcp\"]\n\n"+
		"[mcp_servers.win]\ncommand = 'C:\\tools\\DUOLOG.EXE'\nargs = [\"mcp\"]\n\n"+
		"[mcp_servers.serve]\ncommand = \"duolog\"\nargs = [\"serve\"]\n\n"+
		"[mcp_servers.docs]\ncommand = \"docs\"\nargs = [\"mcp\"]\n")
	s := testSpec(t, root, []string{"HOME=" + home, "CODEX_HOME=" + worker})
	if got := offNames(Worker{}.Argv(s)); !slices.Equal(got, []string{"lead_tools", "win"}) {
		t.Fatalf("worker off %q", got)
	}
	if got := offNames(Pane{}.Argv(s.Route, s.Mode, *s.Process, "")); !slices.Equal(got, []string{"lead_tools", "win"}) {
		t.Fatalf("peer off %q", got)
	}
	// A bridged server that runs duolog mcp under another name too.
	s = testSpec(t, root, []string{"HOME=" + home, "CODEX_HOME=" + t.TempDir()})
	s.MCP = &run.MCP{CodexArgs: []string{"-c", `mcp_servers.tools.command="duolog"`, "-c", `mcp_servers.tools.args=["mcp"]`}}
	if got := offNames(Worker{}.Argv(s)); !slices.Equal(got, []string{"tools"}) {
		t.Fatalf("bridged off %q", got)
	}
}

// A worker environment with CODEX_HOME and no home folder still reads that
// Codex home.
func TestCodexServerOffWithCodexHomeOnly(t *testing.T) {
	_, _, root := capsEnv(t)
	worker := t.TempDir()
	putFile(t, filepath.Join(worker, "config.toml"), ourTable())
	s := testSpec(t, root, []string{"CODEX_HOME=" + worker, "PATH=/usr/bin"})
	if got := offNames(Worker{}.Argv(s)); !slices.Equal(got, []string{"duolog"}) {
		t.Fatalf("worker off %q", got)
	}
	if got := offNames(Pane{}.Argv(s.Route, s.Mode, *s.Process, "")); !slices.Equal(got, []string{"duolog"}) {
		t.Fatalf("peer off %q", got)
	}
}

// A peer reads the project config of the root its process runs for: a
// server defined only in a trusted project's .codex/config.toml is turned
// off too.
func TestCodexPeerTurnsOffProjectServer(t *testing.T) {
	_, home, root := capsEnv(t)
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(home, ".codex", "config.toml"), "[projects."+kit.TOMLString(abs)+"]\ntrust_level = \"trusted\"\n")
	putFile(t, filepath.Join(root, ".codex", "config.toml"), "[mcp_servers.duolog]\ncommand = \"duolog\"\nargs = [\"mcp\"]\n")
	s := testSpec(t, root, []string{"HOME=" + home, "USERPROFILE=" + home})
	p := *s.Process
	if got := offNames(Pane{}.Argv(s.Route, s.Mode, p, "")); len(got) != 0 {
		t.Fatalf("without a root, off %q", got)
	}
	p.Root = root
	if got := offNames(Pane{}.Argv(s.Route, s.Mode, p, "")); !slices.Equal(got, []string{"duolog"}) {
		t.Fatalf("peer off %q", got)
	}
}
