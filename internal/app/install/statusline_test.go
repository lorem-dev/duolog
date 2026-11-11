package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func statusLineDeps(t *testing.T, home string) Deps {
	t.Helper()
	d, _ := leadDeps(t, home, LeadWindow{})
	d.LeadWindow = nil
	return d
}

// statusLineOf reads the statusLine entry of the settings file at path, nil
// when the file or the entry is not there.
func statusLineOf(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	line, _ := doc["statusLine"].(map[string]any)
	return line
}

func isOurStatusLine(line map[string]any) bool {
	return line["type"] == "command" && line["command"] == "duolog statusline" && line["refreshInterval"] == float64(10)
}

func TestInstallWritesStatusLineLocal(t *testing.T) {
	home, root := setup(t)
	d := statusLineDeps(t, home)
	o := Options{Scope: "project", Agent: "claude", Root: root, Home: home}
	r := leadRun(t, d, o)
	if !isOurStatusLine(statusLineOf(t, localSettings(root))) {
		t.Fatalf("local settings: %v", statusLineOf(t, localSettings(root)))
	}
	if statusLineOf(t, filepath.Join(root, ".claude", "settings.json")) != nil {
		t.Fatal("committed settings hold a status line")
	}
	if !slices.Contains(r.Files, localSettings(root)) {
		t.Fatalf("files %v", r.Files)
	}
	if s := claudeStatus(t, d, o); s.Stale() || s.StatusLine == nil || s.StatusLine.Missing {
		t.Fatalf("status %+v", s)
	}
	if again := leadRun(t, d, o); len(again.Files) != 0 {
		t.Fatalf("second install wrote %v", again.Files)
	}
}

func TestInstallKeepsPersonStatusLine(t *testing.T) {
	for _, plugin := range []bool{false, true} {
		home, root := setup(t)
		user := filepath.Join(home, ".claude", "settings.json")
		doc := `{"statusLine":{"type":"command","command":"~/my.sh"}`
		if plugin {
			doc += `,"enabledPlugins":{"duolog@duolog":true}`
		}
		write(t, user, doc+"}")
		d := statusLineDeps(t, home)
		o := Options{Scope: "project", Agent: "claude", Root: root, Home: home}
		r := leadRun(t, d, o)
		if _, err := os.Stat(localSettings(root)); !os.IsNotExist(err) {
			t.Fatalf("plugin %v: local settings written", plugin)
		}
		if !hasNote(r, "claude: your status line in ", "stays; to add duolog's part, run duolog statusline with the same stdin") {
			t.Fatalf("plugin %v: notes %q", plugin, r.Notes)
		}
		s := claudeStatus(t, d, o)
		if s.StatusLine == nil || s.StatusLine.Own == "" || s.StatusLine.Missing {
			t.Fatalf("plugin %v: %+v", plugin, s.StatusLine)
		}
	}
}

func TestStatusLineIsWrittenWithThePlugin(t *testing.T) {
	home, root := setup(t)
	write(t, filepath.Join(home, ".claude", "settings.json"), `{"enabledPlugins":{"duolog@duolog":true}}`)
	d := statusLineDeps(t, home)
	leadRun(t, d, Options{Scope: "project", Agent: "claude", Root: root, Home: home})
	if !isOurStatusLine(statusLineOf(t, localSettings(root))) {
		t.Fatal("status line not written with the plugin")
	}
}

func TestUserInstallStatusLine(t *testing.T) {
	home, root := setup(t)
	d := statusLineDeps(t, home)
	o := Options{Scope: "user", Agent: "claude", Root: root, Home: home}
	leadRun(t, d, o)
	user := filepath.Join(home, ".claude", "settings.json")
	if !isOurStatusLine(statusLineOf(t, user)) {
		t.Fatalf("user settings: %v", statusLineOf(t, user))
	}
	if _, err := os.Stat(localSettings(root)); !os.IsNotExist(err) {
		t.Fatal("local settings written at user scope")
	}
	if s := claudeStatus(t, d, o); s.StatusLine == nil || s.StatusLine.Missing {
		t.Fatalf("status %+v", s.StatusLine)
	}
	if _, err := d.Uninstall(o); err != nil {
		t.Fatal(err)
	}
	if statusLineOf(t, user) != nil {
		t.Fatal("user status line left")
	}
}

func TestUninstallRemovesOnlyOurStatusLine(t *testing.T) {
	home, root := setup(t)
	d := statusLineDeps(t, home)
	o := Options{Scope: "project", Agent: "claude", Root: root, Home: home}
	leadRun(t, d, o)
	if _, err := d.Uninstall(o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(localSettings(root)); !os.IsNotExist(err) {
		t.Fatalf("local settings left: %v", err)
	}
	// A file the person had keeps its other keys.
	write(t, localSettings(root), `{"env":{"KEEP":"1"}}`)
	leadRun(t, d, o)
	if _, err := d.Uninstall(o); err != nil {
		t.Fatal(err)
	}
	if doc, env := localEnv(t, root); env["KEEP"] != "1" || doc["statusLine"] != nil {
		t.Fatalf("doc %v", doc)
	}
	// The person's status line stays.
	write(t, localSettings(root), `{"statusLine":{"type":"command","command":"~/my.sh"}}`)
	leadRun(t, d, o)
	if _, err := d.Uninstall(o); err != nil {
		t.Fatal(err)
	}
	if line := statusLineOf(t, localSettings(root)); line["command"] != "~/my.sh" {
		t.Fatalf("status line %v", line)
	}
}

func TestCheckStatusLine(t *testing.T) {
	home, root := setup(t)
	o := Options{Scope: "project", Agent: "claude", Root: root, Home: home}
	// An install by a duolog that wrote no status line.
	if _, err := testDeps().Run(o); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{localSettings(root), statusLineMarkerPath(root)} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	d := statusLineDeps(t, home)
	s := claudeStatus(t, d, o)
	if !s.Stale() || !strings.Contains(s.Line(root), "status line not set in "+filepath.Join(".claude", "settings.local.json")) {
		t.Fatalf("stale %v line %q", s.Stale(), s.Line(root))
	}
	write(t, filepath.Join(home, ".claude", "settings.json"), `{"statusLine":{"type":"command","command":"~/my.sh"}}`)
	s = claudeStatus(t, d, o)
	if s.Stale() {
		t.Fatalf("stale with the person's status line: %q", s.Line(root))
	}
	if !strings.Contains(s.StatusLineLine(root), "claude: status line is yours (") {
		t.Fatalf("line %q", s.StatusLineLine(root))
	}
}

func TestStatusLineOfThePersonIsAnInfoLine(t *testing.T) {
	home, root := setup(t)
	write(t, filepath.Join(home, ".claude", "settings.json"), `{"statusLine":{"type":"command","command":"~/my.sh"}}`)
	d := statusLineDeps(t, home)
	o := Options{Scope: "project", Agent: "claude", Root: root, Home: home}
	leadRun(t, d, o)
	s := claudeStatus(t, d, o)
	if slices.ContainsFunc(s.Lines(root), func(l string) bool { return strings.Contains(l, "status line is yours") }) {
		t.Fatalf("doctor warns: %q", s.Lines(root))
	}
	if line := s.StatusLineLine(root); !strings.Contains(line, "claude: status line is yours (") {
		t.Fatalf("line %q", line)
	}
	if line := d.StatusLineLine(root, home); !strings.Contains(line, "status line is yours (") {
		t.Fatalf("deps line %q", line)
	}
}

func TestPersonsLaterStatusLineIsHiddenByOurs(t *testing.T) {
	home, root := setup(t)
	d := statusLineDeps(t, home)
	o := Options{Scope: "project", Agent: "claude", Root: root, Home: home}
	leadRun(t, d, o)
	user := filepath.Join(home, ".claude", "settings.json")
	write(t, user, `{"statusLine":{"type":"command","command":"~/my.sh"}}`)
	s := claudeStatus(t, d, o)
	if !s.Stale() || !strings.Contains(s.Line(root), "duolog's status line in "+filepath.Join(".claude", "settings.local.json")+" hides yours in ") {
		t.Fatalf("stale %v line %q", s.Stale(), s.Line(root))
	}
	leadRun(t, d, o)
	if statusLineOf(t, localSettings(root)) != nil {
		t.Fatal("our status line still hides the person's")
	}
	if line := statusLineOf(t, user); line["command"] != "~/my.sh" {
		t.Fatalf("person's status line %v", line)
	}
	if s := claudeStatus(t, d, o); s.Stale() {
		t.Fatalf("stale after install: %q", s.Line(root))
	}
}

func TestRemovedStatusLineStaysOffUntilAsked(t *testing.T) {
	home, root := setup(t)
	d := statusLineDeps(t, home)
	o := Options{Scope: "project", Agent: "claude", Root: root, Home: home}
	write(t, localSettings(root), `{"env":{"A":"1"}}`)
	leadRun(t, d, o)
	// The person removes it.
	write(t, localSettings(root), `{"env":{"A":"1"}}`)
	s := claudeStatus(t, d, o)
	if s.Stale() || s.StatusLine == nil || !s.StatusLine.Declined {
		t.Fatalf("status %+v stale %v", s.StatusLine, s.Stale())
	}
	if line := s.StatusLineLine(root); !strings.Contains(line, "duolog install --status-line") {
		t.Fatalf("line %q", line)
	}
	leadRun(t, d, o)
	if statusLineOf(t, localSettings(root)) != nil {
		t.Fatal("status line added again")
	}
	if s := claudeStatus(t, d, o); s.Stale() || !s.StatusLine.Declined {
		t.Fatalf("status %+v", s.StatusLine)
	}
	o.StatusLine = true
	r := leadRun(t, d, o)
	if !isOurStatusLine(statusLineOf(t, localSettings(root))) || !slices.Contains(r.Files, localSettings(root)) {
		t.Fatalf("status line not added back: %v", r.Files)
	}
	o.StatusLine = false
	if s := claudeStatus(t, d, o); s.Stale() || s.StatusLine.Declined || s.StatusLine.Missing {
		t.Fatalf("status %+v", s.StatusLine)
	}
}

func TestUninstallRemovesAFileAnOlderInstallCreated(t *testing.T) {
	home, root := setup(t)
	d := statusLineDeps(t, home)
	o := Options{Scope: "project", Agent: "claude", Root: root, Home: home}
	leadRun(t, d, o)
	// An older install: the lead marker says it created the file, and there
	// is no status line marker.
	if err := os.Remove(statusLineMarkerPath(root)); err != nil {
		t.Fatal(err)
	}
	write(t, markerPath(root), `{"window":0,"created":true}`)
	if _, err := d.Uninstall(o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(localSettings(root)); !os.IsNotExist(err) {
		t.Fatalf("local settings left: %v", err)
	}
}

func TestUninstallKeepsAPersonsEmptyFile(t *testing.T) {
	home, root := setup(t)
	d := statusLineDeps(t, home)
	o := Options{Scope: "project", Agent: "claude", Root: root, Home: home}
	write(t, localSettings(root), `{}`)
	leadRun(t, d, o)
	if _, err := d.Uninstall(o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(localSettings(root)); err != nil {
		t.Fatalf("the person's file is gone: %v", err)
	}
}

func TestStatusLineRefusesWhatItCannotRead(t *testing.T) {
	for name, content := range map[string]string{"invalid": `{"env":`, "array": `[1]`} {
		home, root := setup(t)
		write(t, localSettings(root), content)
		d := statusLineDeps(t, home)
		if _, err := d.Run(Options{Scope: "project", Agent: "claude", Root: root, Home: home}); err == nil {
			t.Fatalf("%s: no error", name)
		}
		if got := read(t, localSettings(root)); got != content {
			t.Fatalf("%s: file changed to %q", name, got)
		}
	}
	home, root := setup(t)
	content := `{"statusLine":null}`
	write(t, localSettings(root), content)
	r := leadRun(t, statusLineDeps(t, home), Options{Scope: "project", Agent: "claude", Root: root, Home: home})
	// The entry counts as the person's: refused, with a note, file untouched.
	if !hasNote(r, "claude: your status line in ", "stays") || slices.Contains(r.Files, localSettings(root)) {
		t.Fatalf("null: notes %q files %v", r.Notes, r.Files)
	}
	if got := read(t, localSettings(root)); got != content {
		t.Fatalf("null: file changed to %q", got)
	}
}

func TestUserUninstallKeepsOtherKeys(t *testing.T) {
	home, root := setup(t)
	user := filepath.Join(home, ".claude", "settings.json")
	write(t, user, `{"env":{"KEEP":"1"}}`)
	d := statusLineDeps(t, home)
	o := Options{Scope: "user", Agent: "claude", Root: root, Home: home}
	leadRun(t, d, o)
	if _, err := d.Uninstall(o); err != nil {
		t.Fatal(err)
	}
	data := read(t, user)
	if !strings.Contains(data, `"KEEP"`) || strings.Contains(data, "statusLine") {
		t.Fatalf("settings %s", data)
	}
}

func TestDroppedPersonLineDoesNotBlockOurs(t *testing.T) {
	home, root := setup(t)
	d := statusLineDeps(t, home)
	o := Options{Scope: "project", Agent: "claude", Root: root, Home: home}
	leadRun(t, d, o)
	user := filepath.Join(home, ".claude", "settings.json")
	write(t, user, `{"statusLine":{"type":"command","command":"~/my.sh"}}`)
	// Install removes ours, and the file it created with it.
	leadRun(t, d, o)
	if _, err := os.Stat(localSettings(root)); !os.IsNotExist(err) {
		t.Fatalf("local settings left: %v", err)
	}
	write(t, user, `{}`)
	s := claudeStatus(t, d, o)
	if s.StatusLine.Declined || !s.StatusLine.Missing {
		t.Fatalf("status %+v", s.StatusLine)
	}
	leadRun(t, d, o)
	if !isOurStatusLine(statusLineOf(t, localSettings(root))) {
		t.Fatal("status line not added once the person's is gone")
	}
}

func TestRemoveKeepsAFileThatChanged(t *testing.T) {
	home, root := setup(t)
	d := statusLineDeps(t, home)
	o := Options{Scope: "project", Agent: "claude", Root: root, Home: home}
	leadRun(t, d, o)
	a := d.Agents[0]
	st, err := d.readStatusLine(a, o)
	if err != nil {
		t.Fatal(err)
	}
	// Someone writes to the file after it was read.
	write(t, localSettings(root), `{"env":{"NEW":"1"},"statusLine":{"type":"command","command":"duolog statusline"}}`)
	changed, err := setStatusLine(a, st, o, false, false)
	if err != nil || changed {
		t.Fatalf("changed %v err %v", changed, err)
	}
	if got := read(t, localSettings(root)); !strings.Contains(got, "NEW") {
		t.Fatalf("file lost: %q", got)
	}
}
