package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// check runs Check on testDeps and fails the test on an error.
func check(t *testing.T, o Options) map[string]Status {
	t.Helper()
	statuses, err := testDeps().Check(o)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Status{}
	for _, s := range statuses {
		out[s.Agent] = s
	}
	return out
}

// installed runs a project install for both agents and returns its options.
func installed(t *testing.T) (Options, string) {
	t.Helper()
	home, root := setup(t)
	o := Options{Scope: "project", Agent: "both", Home: home, Root: root}
	run(t, o)
	return o, root
}

// rel lists paths relative to root, with slashes.
func rel(t *testing.T, root string, paths []string) []string {
	t.Helper()
	var out []string
	for _, p := range paths {
		r, err := filepath.Rel(root, p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, filepath.ToSlash(r))
	}
	return out
}

func current(t *testing.T, s Status) {
	t.Helper()
	if !s.Installed || s.Stale() || s.Err != nil {
		t.Fatalf("%s is not current: %+v", s.Agent, s)
	}
}

func TestCheckFreshInstallIsCurrent(t *testing.T) {
	o, root := installed(t)
	before, home := snapshot(t, root), snapshot(t, o.Home)
	got := check(t, o)
	if len(got) != 2 {
		t.Fatalf("statuses: %+v", got)
	}
	current(t, got["claude"])
	current(t, got["codex"])
	if !equal(before, snapshot(t, root)) || !equal(home, snapshot(t, o.Home)) {
		t.Fatal("Check wrote a file")
	}
	for _, s := range got {
		if line := s.Line(root); line != "" {
			t.Fatalf("a current install has a line: %q", line)
		}
	}
}

// A skill the person edited is theirs: install leaves it, so it is a line
// for the person and not an install that is out of date.
func TestCheckEditedSkillIsLeft(t *testing.T) {
	o, _ := installed(t)
	path := userSkill(o.Home, "claude", "duolog")
	write(t, path, read(t, path)+"\nA line added by hand.\n")
	before := snapshot(t, o.Home)
	got := check(t, o)
	if s := got["claude"]; s.Stale() || !slices.Equal(s.Edited, []string{filepath.Dir(path)}) {
		t.Fatalf("status %+v", s)
	}
	current(t, got["codex"])
	if !equal(before, snapshot(t, o.Home)) {
		t.Fatal("Check wrote a file")
	}
}

func TestCheckMissingSkillIsStale(t *testing.T) {
	o, _ := installed(t)
	if err := os.RemoveAll(filepath.Join(o.Home, ".agents", "skills", "duolog-peers")); err != nil {
		t.Fatal(err)
	}
	got := check(t, o)
	if changed := rel(t, o.Home, got["codex"].Changed); len(changed) != 1 || changed[0] != ".agents/skills/duolog-peers/SKILL.md" {
		t.Fatalf("changed: %v", changed)
	}
	current(t, got["claude"])
}

func TestCheckRemovedStopHookIsStale(t *testing.T) {
	o, root := installed(t)
	path := filepath.Join(root, ".claude", "settings.json")
	var doc map[string]map[string]any
	if err := json.Unmarshal([]byte(read(t, path)), &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc["hooks"], "Stop")
	data, _ := json.Marshal(doc)
	write(t, path, string(data))
	got := check(t, o)
	if changed := rel(t, root, got["claude"].Changed); len(changed) != 1 || changed[0] != ".claude/settings.json" {
		t.Fatalf("changed: %v", changed)
	}
}

func TestCheckIgnoresForeignHooksSettingsAndSkills(t *testing.T) {
	o, root := installed(t)
	// Another Stop hook, another setting and a compact layout of the file are
	// the person's, not a difference.
	path := filepath.Join(root, ".claude", "settings.json")
	var doc map[string]any
	if err := json.Unmarshal([]byte(read(t, path)), &doc); err != nil {
		t.Fatal(err)
	}
	doc["model"] = "opus"
	hooks := doc["hooks"].(map[string]any)
	hooks["Stop"] = append(hooks["Stop"].([]any), map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "other-stop"}}})
	hooks["Notification"] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "other-note"}}}}
	data, _ := json.Marshal(doc)
	write(t, path, string(data))
	codex := filepath.Join(root, ".codex", "hooks.json")
	write(t, codex, strings.Replace(read(t, codex), `"hooks": {`, `"hooks": {"Notification": [{"hooks": [{"type": "command", "command": "other"}]}],`, 1))
	// A skill that is not duolog's, and an install backup or a folder file the
	// system adds inside duolog's own.
	write(t, filepath.Join(o.Home, ".claude", "skills", "other", "SKILL.md"), "other\n")
	write(t, filepath.Join(root, ".agents", "skills", "mine", "SKILL.md"), "mine\n")
	write(t, filepath.Join(o.Home, ".claude", "skills", "duolog", "SKILL.md"+backupSuffix), "old\n")
	write(t, filepath.Join(o.Home, ".claude", "skills", "duolog", ".DS_Store"), "x")
	// The files an editor leaves beside the one it edits.
	for _, name := range []string{"SKILL.md~", ".SKILL.md.swp", "#SKILL.md#"} {
		write(t, filepath.Join(o.Home, ".agents", "skills", "duolog", name), "x")
	}
	got := check(t, o)
	current(t, got["claude"])
	current(t, got["codex"])
	for _, s := range got {
		if lines := s.Lines(root); len(lines) != 0 {
			t.Fatalf("%s: %q", s.Agent, lines)
		}
	}
}

// install never removes a file it does not ship, here one an earlier duolog
// shipped, so such a file is a line for the person, and not an install that
// is out of date.
func TestCheckExtraFileInDuologSkillIsNotStale(t *testing.T) {
	o, root := installed(t)
	notes := filepath.Join(o.Home, ".claude", "skills", "duolog-calibrate", "notes.md")
	withKnown(t, "what an earlier duolog shipped\n")
	write(t, notes, "what an earlier duolog shipped\n")
	got := check(t, o)
	s := got["claude"]
	if extra := rel(t, o.Home, s.Extra); len(extra) != 1 || extra[0] != ".claude/skills/duolog-calibrate/notes.md" || len(s.Changed) != 0 {
		t.Fatalf("%+v", s)
	}
	current(t, s)
	if line := s.Line(root); line != "" {
		t.Fatalf("line %q", line)
	}
	want := []string{"claude project install: remove " + notes + ", which duolog does not ship"}
	if lines := s.Lines(root); !slices.Equal(lines, want) {
		t.Fatalf("lines %q, want %q", lines, want)
	}
	if note := testDeps().ProjectNote(root, o.Home); note != "" {
		t.Fatalf("extra files gave a note: %q", note)
	}
	if lines := testDeps().StaleLines(root, o.Home); !slices.Equal(lines, want) {
		t.Fatalf("doctor lines %q", lines)
	}
}

// When two agents' skills folders are one directory, each finds the other's
// duolog skills there. That is no stale install: install cannot fix it.
func TestCheckSharedSkillsFolder(t *testing.T) {
	o, root := installed(t)
	codex := filepath.Join(o.Home, ".agents", "skills")
	if err := os.RemoveAll(codex); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", ".claude", "skills"), codex); err != nil {
		t.Skip("no symbolic links here:", err)
	}
	got := check(t, o)
	current(t, got["claude"])
	current(t, got["codex"])
	want := "claude and codex share " + filepath.Join(o.Home, ".claude", "skills") + "; duolog writes each agent's own skills there, so keep them apart"
	var lines []string
	for _, agent := range []string{"claude", "codex"} {
		lines = append(lines, got[agent].Lines(root)...)
	}
	if !slices.Equal(lines, []string{want}) {
		t.Fatalf("lines %q, want %q", lines, want)
	}
	if note := testDeps().ProjectNote(root, o.Home); note != "" {
		t.Fatalf("note %q", note)
	}
	// One agent alone shares with no selected agent.
	o.Agent = "codex"
	if s := check(t, o)["codex"]; !s.Stale() {
		t.Fatalf("codex alone: %+v", s)
	}
}

// A repository that commits duolog's hooks and ignores its skills checks the
// hooks alone in CI.
func TestCheckHooksOnly(t *testing.T) {
	o, root := installed(t)
	for _, dir := range []string{filepath.Join(o.Home, ".claude", "skills"), filepath.Join(o.Home, ".agents", "skills")} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(root, ".agents", "skills", "duolog", "SKILL.md"), "old\n")
	o.HooksOnly = true
	got := check(t, o)
	current(t, got["claude"])
	current(t, got["codex"])
	path := filepath.Join(root, ".codex", "hooks.json")
	var doc map[string]map[string]any
	if err := json.Unmarshal([]byte(read(t, path)), &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc["hooks"], "Stop")
	data, _ := json.Marshal(doc)
	write(t, path, string(data))
	if changed := rel(t, root, check(t, o)["codex"].Changed); len(changed) != 1 || changed[0] != ".codex/hooks.json" {
		t.Fatalf("changed %v", changed)
	}
	// Without hooks there is no install to check.
	write(t, path, `{"hooks":{}}`)
	if s := check(t, o)["codex"]; s.Installed {
		t.Fatalf("%+v", s)
	}
}

// A repository can commit duolog's hooks. A fresh clone on a machine that
// never ran duolog install has the hooks and none of the user skills: that
// is a stale install, not a missing one.
func TestCheckHooksWithoutSkillsIsStale(t *testing.T) {
	o, root := installed(t)
	for _, dir := range []string{filepath.Join(o.Home, ".claude", "skills"), filepath.Join(o.Home, ".agents", "skills")} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	got := check(t, o)
	for agent, dir := range map[string]string{"claude": ".claude/skills", "codex": ".agents/skills"} {
		s := got[agent]
		if !s.Installed || !s.Stale() {
			t.Fatalf("%s: %+v", agent, s)
		}
		var want []string
		for _, name := range []string{"duolog", "duolog-skill-check", "duolog-calibrate", "duolog-peers", "duolog-update"} {
			want = append(want, filepath.Join(o.Home, filepath.FromSlash(dir), name))
		}
		line := agent + " project install is out of date: duolog skills missing: " + strings.Join(want, ", ") + "; run duolog install --scope project --agent " + agent
		if got := s.Line(root); got != line {
			t.Fatalf("line %q, want %q", got, line)
		}
	}
	note := testDeps().ProjectNote(root, o.Home)
	if note != "duolog: the duolog skills are missing; run duolog install --scope project" {
		t.Fatalf("note %q", note)
	}
}

func TestProjectNote(t *testing.T) {
	o, root := installed(t)
	if note := testDeps().ProjectNote(root, o.Home); note != "" {
		t.Fatalf("a current install has a note: %q", note)
	}
	write(t, filepath.Join(root, ".codex", "hooks.json"), `{"hooks":{}}`+"\n")
	path := userSkill(o.Home, "claude", "duolog")
	older := strings.Replace(read(t, path), Version(), "1.0.0", 1) + "older text\n"
	withKnown(t, older)
	write(t, path, older)
	if note := testDeps().ProjectNote(root, o.Home); note != "duolog: this project's duolog install is out of date; run duolog install --scope project" {
		t.Fatalf("note %q", note)
	}
}

func TestProjectNoteSilentOnError(t *testing.T) {
	o, root := installed(t)
	write(t, filepath.Join(root, ".claude", "settings.json"), "not json")
	if note := testDeps().ProjectNote(root, o.Home); note != "" {
		t.Fatalf("a failed check has a note: %q", note)
	}
}

// duolog's own repository runs its hooks through the dev launcher of the
// checkout; install would replace them, so they are not compared.
func TestCheckDevLauncherHooksAreNotCompared(t *testing.T) {
	o, root := installed(t)
	settings := filepath.Join(root, ".claude", "settings.json")
	dev := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"exec \"$CLAUDE_PROJECT_DIR\"/scripts/dev/bin/duolog hook Stop --agent claude"}]}]}}`
	write(t, settings, dev)
	if s := check(t, o)["claude"]; !s.Stale() || s.Dev {
		t.Fatalf("dev hooks without the launcher: %+v", s)
	}
	write(t, filepath.Join(root, "scripts", "dev", "bin", "duolog"), "#!/bin/sh\n# duolog built from this checkout.\nexit 0\n")
	s := check(t, o)["claude"]
	if !s.Installed || !s.Dev || s.Stale() || s.Line(root) != "" {
		t.Fatalf("%+v", s)
	}
	if note := testDeps().ProjectNote(root, o.Home); note != "" {
		t.Fatalf("note %q", note)
	}
}

func TestCheckLineNamesFilesAndFix(t *testing.T) {
	o, root := installed(t)
	skill := userSkill(o.Home, "claude", "duolog")
	withKnown(t, "---\nmetadata:\n  duolog-version: \"1.0.0\"\n---\nold skill\n")
	write(t, skill, "---\nmetadata:\n  duolog-version: \"1.0.0\"\n---\nold skill\n")
	settings := filepath.Join(root, ".claude", "settings.json")
	write(t, settings, `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"duolog","args":["hook","Stop","--agent","claude"]}]}]}}`)
	got := check(t, o)
	want := "claude project install is out of date: " + filepath.Join(".claude", "settings.json") + ", " + skill + "; run duolog install --scope project --agent claude"
	if line := got["claude"].Line(root); line != want {
		t.Fatalf("line %q, want %q", line, want)
	}
}

func TestCheckCRLFSkillIsCurrent(t *testing.T) {
	o, _ := installed(t)
	// A skill copied on Windows can have CRLF line endings.
	path := userSkill(o.Home, "codex", "duolog")
	write(t, path, strings.ReplaceAll(read(t, path), "\n", "\r\n"))
	current(t, check(t, o)["codex"])
}

func TestCheckNotInstalledIsNotStale(t *testing.T) {
	home, root := setup(t)
	// A foreign skill and foreign hooks alone are no duolog install.
	write(t, filepath.Join(root, ".claude", "settings.json"), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"other"}]}]}}`)
	write(t, filepath.Join(root, ".agents", "skills", "other", "SKILL.md"), "other\n")
	before := snapshot(t, root)
	for agent, s := range check(t, Options{Scope: "project", Agent: "both", Home: home, Root: root}) {
		if s.Installed || s.Stale() || s.Err != nil || s.Line(root) != "" {
			t.Fatalf("%s: %+v", agent, s)
		}
	}
	if !equal(before, snapshot(t, root)) {
		t.Fatal("Check wrote a file")
	}
}

func TestCheckUserScope(t *testing.T) {
	home, root := setup(t)
	o := Options{Scope: "user", Agent: "codex", Home: home, Root: root}
	run(t, o)
	got := check(t, o)
	if len(got) != 1 {
		t.Fatalf("statuses: %+v", got)
	}
	current(t, got["codex"])
	if err := os.Remove(filepath.Join(home, ".agents", "skills", "duolog", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	s := check(t, o)["codex"]
	if !s.Stale() || !strings.HasSuffix(s.Line(root), "; run duolog install --scope user --agent codex") {
		t.Fatalf("%+v %q", s, s.Line(root))
	}
}

func TestCheckBadHooksFileIsAnError(t *testing.T) {
	o, root := installed(t)
	write(t, filepath.Join(root, ".codex", "hooks.json"), `{"hooks": "duolog`)
	s := check(t, o)["codex"]
	if s.Err == nil || !strings.HasPrefix(s.Line(root), "codex project install could not be checked: ") {
		t.Fatalf("%+v %q", s, s.Line(root))
	}
}

func TestCheckClaudePluginEnabledIsCurrent(t *testing.T) {
	o, root := installed(t)
	// With the plugin on, install removes the hooks from settings.json; a
	// settings file without them is what it would write.
	path := filepath.Join(root, ".claude", "settings.json")
	withHooks := strings.Replace(read(t, path), "{", `{"enabledPlugins":{"duolog@duolog":true},`, 1)
	// install still writes the MCP allow entries beside the plugin.
	write(t, path, `{"enabledPlugins":{"duolog@duolog":true},"permissions":{"allow":["mcp__duolog__delegate","mcp__duolog__peer","mcp__duolog__status"]}}`)
	current(t, check(t, o)["claude"])
	// Committed hooks beside the plugin run twice. install would delete them
	// from a shared file, so this is a line for the person, not a stale
	// install.
	write(t, path, withHooks)
	s := check(t, o)["claude"]
	current(t, s)
	want := []string{"claude: duolog hooks are in " + filepath.Join(".claude", "settings.json") + " and the duolog plugin is enabled; they run twice"}
	if lines := s.Lines(root); !slices.Equal(lines, want) {
		t.Fatalf("lines %q, want %q", lines, want)
	}
	if note := testDeps().ProjectNote(root, o.Home); note != "" {
		t.Fatalf("note %q", note)
	}
}
