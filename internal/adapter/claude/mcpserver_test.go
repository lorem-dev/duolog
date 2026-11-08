package claude

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// isolateHome points every home-derived variable into the test's temporary
// folder, so nothing reaches the real Claude config.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude-config"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	return home
}

// allowNames are the allow entries install passes (mcp.AllowedTools).
var allowNames = []string{"mcp__duolog__delegate", "mcp__duolog__peer", "mcp__duolog__status"}

// stateFixture is a user state file as Claude writes it, with keys duolog
// must not touch: a project, an OAuth account and a number in a form that a
// round trip through encoding/json would rewrite.
const stateFixture = `{
  "numStartups": 1e3,
  "oauthAccount": {"emailAddress": "person@example.invalid",   "uuid": "u-1"},
  "projects": {
    "/work/app": {
      "mcpServers": {"local": {"command": "x"}},
      "allowedTools": []
    }
  },
  "tipsHistory": {"a": 1.50}
}
`

// topLevel returns the raw bytes of each top-level key of data.
func topLevel(t *testing.T, data []byte) map[string]string {
	t.Helper()
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("%s: %v", data, err)
	}
	out := map[string]string{}
	for k, v := range top {
		out[k] = string(v)
	}
	return out
}

func TestServerAddKeepsOtherKeysByteForByte(t *testing.T) {
	isolateHome(t)
	got, note, err := Installer{}.Server([]byte(stateFixture), true)
	if err != nil || note != "" {
		t.Fatalf("note %q, err %v", note, err)
	}
	before, after := topLevel(t, []byte(stateFixture)), topLevel(t, got)
	for key, raw := range before {
		if after[key] != raw {
			t.Errorf("key %s changed: %q, was %q", key, after[key], raw)
		}
	}
	if len(after) != len(before)+1 {
		t.Fatalf("keys %v", after)
	}
	var servers map[string]struct {
		Type    string   `json:"type"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	if err := json.Unmarshal([]byte(after["mcpServers"]), &servers); err != nil {
		t.Fatal(err)
	}
	if s := servers["duolog"]; len(servers) != 1 || s.Type != "stdio" || s.Command != "duolog" || !slices.Equal(s.Args, []string{"mcp"}) {
		t.Fatalf("servers %+v", servers)
	}
	// The text before the new key is the file as it was, up to its last value.
	if !strings.HasPrefix(string(got), strings.TrimRight(stateFixture, "}\n")) {
		t.Fatalf("the file was rewritten:\n%s", got)
	}
	// A file that already lists other servers keeps them and their bytes.
	withOther := strings.Replace(stateFixture, `"numStartups": 1e3,`, `"numStartups": 1e3,
  "mcpServers": {
    "other": {"command": "other-server",  "args": []}
  },`, 1)
	got, _, err = Installer{}.Server([]byte(withOther), true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"other": {"command": "other-server",  "args": []}`) || !strings.Contains(string(got), `"duolog"`) {
		t.Fatalf("got\n%s", got)
	}
	// A missing file gets a new one with the entry alone.
	got, _, err = Installer{}.Server(nil, true)
	if err != nil || len(topLevel(t, got)) != 1 {
		t.Fatalf("got %s, err %v", got, err)
	}
}

func TestServerAddIsIdempotent(t *testing.T) {
	isolateHome(t)
	once, _, err := Installer{}.Server([]byte(stateFixture), true)
	if err != nil {
		t.Fatal(err)
	}
	twice, _, err := Installer{}.Server(once, true)
	if err != nil || string(twice) != string(once) {
		t.Fatalf("second add changed the file:\n%s\n%v", twice, err)
	}
	// Claude may re-indent the file; the same entry in another layout is ours.
	reindented := `{"mcpServers": {"duolog": {
      "args": [ "mcp" ],
      "command": "duolog",
      "type": "stdio"
    }}}`
	same, _, err := Installer{}.Server([]byte(reindented), true)
	if err != nil || string(same) != reindented {
		t.Fatalf("got %s, err %v", same, err)
	}
	// Remove after add gives back the bytes before.
	removed, _, err := Installer{}.Server(once, false)
	if err != nil || string(removed) != stateFixture {
		t.Fatalf("remove gave\n%s\nwant\n%s (%v)", removed, stateFixture, err)
	}
}

func TestServerRemoveOnlyOurs(t *testing.T) {
	isolateHome(t)
	theirs := `{"mcpServers": {"duolog": {"type": "stdio", "command": "/opt/tools/duolog-fork", "args": ["mcp"]}}}`
	got, note, err := Installer{}.Server([]byte(theirs), false)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != theirs {
		t.Fatalf("an entry that is not duolog's was changed:\n%s", got)
	}
	if !strings.Contains(note, "mcpServers.duolog") || !strings.Contains(note, "stays") {
		t.Fatalf("note %q", note)
	}
	// Removing from a file without the entry changes nothing.
	got, note, err = Installer{}.Server([]byte(stateFixture), false)
	if err != nil || note != "" || string(got) != stateFixture {
		t.Fatalf("got %s, note %q, err %v", got, note, err)
	}
	// Our entry beside another server goes, and the other stays.
	both := `{"mcpServers": {"other": {"command": "x"}, "duolog": {"type":"stdio","command":"duolog","args":["mcp"]}}}`
	got, note, err = Installer{}.Server([]byte(both), false)
	if err != nil || note != "" || string(got) != `{"mcpServers": {"other": {"command": "x"}}}` {
		t.Fatalf("got %s, note %q, err %v", got, note, err)
	}
	// Add leaves an entry that runs something else too, and says so.
	added, note, err := Installer{}.Server([]byte(theirs), true)
	if err != nil || string(added) != theirs || note != "mcpServers.duolog runs something else; rename it" {
		t.Fatalf("got %s, note %q, err %v", added, note, err)
	}
	// An entry that runs duolog mcp in another form is duolog's, and add
	// writes it as install does.
	old := `{"mcpServers": {"duolog": {"command": "duolog", "args": ["mcp"]}}}`
	added, note, err = Installer{}.Server([]byte(old), true)
	if err != nil || note != "" || string(added) != `{"mcpServers": {"duolog": `+mcpEntry+`}}` {
		t.Fatalf("got %s, note %q, err %v", added, note, err)
	}
}

func TestServerRefusesUnparsableFile(t *testing.T) {
	isolateHome(t)
	for _, bad := range []string{`{"projects": {`, `[]`, `null`, ``, `{"mcpServers": []}`, `{} {}`} {
		for _, add := range []bool{true, false} {
			if got, _, err := (Installer{}).Server([]byte(bad), add); err == nil {
				t.Errorf("%q (add %v): no error, got %s", bad, add, got)
			}
		}
	}
}

func TestAllowAddsThreeNotAnswerAsk(t *testing.T) {
	isolateHome(t)
	existing := []byte(`{"model": "x", "permissions": {"allow": ["Bash(make:*)"], "deny": ["Read(.env)"]}}`)
	got, err := Installer{}.Allow(existing, allowNames, true)
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Model       string `json:"model"`
		Permissions struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(got, &s); err != nil {
		t.Fatal(err)
	}
	want := append([]string{"Bash(make:*)"}, allowNames...)
	if !slices.Equal(s.Permissions.Allow, want) || s.Model != "x" || !slices.Equal(s.Permissions.Deny, []string{"Read(.env)"}) {
		t.Fatalf("got %s", got)
	}
	if strings.Contains(string(got), "answer_ask") || slices.Contains(s.Permissions.Allow, "mcp__duolog") || strings.Contains(string(got), "enableAllProjectMcpServers") {
		t.Fatalf("got %s", got)
	}
	again, err := Installer{}.Allow(got, allowNames, true)
	if err != nil || string(again) != string(got) {
		t.Fatalf("second add changed the file: %s %v", again, err)
	}
	created, err := Installer{}.Allow(nil, allowNames, true)
	if err != nil || !strings.Contains(string(created), "mcp__duolog__status") {
		t.Fatalf("got %s, err %v", created, err)
	}
	if _, err := (Installer{}).Allow([]byte(`{"permissions": []}`), allowNames, true); err == nil {
		t.Fatal("a permissions value that is not an object was overwritten")
	}
}

func TestAllowRemoveLeavesPersonsEntries(t *testing.T) {
	isolateHome(t)
	existing := []byte("{\n  \"model\": \"x\"\n}\n")
	added, err := Installer{}.Allow(existing, allowNames, true)
	if err != nil {
		t.Fatal(err)
	}
	removed, err := Installer{}.Allow(added, allowNames, false)
	if err != nil || string(removed) != string(existing) {
		t.Fatalf("got %q, want %q (%v)", removed, existing, err)
	}
	persons := []byte(`{"permissions": {"allow": ["mcp__duolog__peer", "mcp__duolog__answer_ask", "Bash(ls)"]}}`)
	got, err := Installer{}.Allow(persons, allowNames, false)
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(got, &s); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.Permissions.Allow, []string{"mcp__duolog__answer_ask", "Bash(ls)"}) {
		t.Fatalf("got %s", got)
	}
	if same, err := (Installer{}).Allow(nil, allowNames, false); err != nil || same != nil {
		t.Fatalf("removing from no file gave %q, %v", same, err)
	}
}

func TestServerFileHonoursClaudeConfigDir(t *testing.T) {
	home := isolateHome(t)
	dir := filepath.Join(home, "elsewhere")
	if got := (Installer{}).ServerFile(home, []string{"CLAUDE_CONFIG_DIR=" + dir}); got != filepath.Join(dir, ".claude.json") {
		t.Fatalf("got %s", got)
	}
	if got := (Installer{}).ServerFile(home, []string{"HOME=" + home}); got != filepath.Join(home, ".claude.json") {
		t.Fatalf("got %s", got)
	}
}

func TestDeferredOff(t *testing.T) {
	home := isolateHome(t)
	root := t.TempDir()
	if (Installer{}).DeferredOff(root, home, nil) {
		t.Fatal("off without a setting")
	}
	if !(Installer{}).DeferredOff(root, home, []string{"ENABLE_TOOL_SEARCH=false"}) {
		t.Fatal("ENABLE_TOOL_SEARCH=false in the process is not off")
	}
	if (Installer{}).DeferredOff(root, home, []string{"ENABLE_TOOL_SEARCH=true"}) {
		t.Fatal("true is off")
	}
	writeFile(t, filepath.Join(root, ".claude", "settings.json"), `{"env": {"ENABLE_TOOL_SEARCH": "false"}}`)
	if !(Installer{}).DeferredOff(root, home, nil) {
		t.Fatal("the project settings env is not read")
	}
	// The local settings apply last.
	writeFile(t, filepath.Join(root, ".claude", "settings.local.json"), `{"env": {"ENABLE_TOOL_SEARCH": "auto"}}`)
	if (Installer{}).DeferredOff(root, home, nil) {
		t.Fatal("local settings did not win")
	}
}
