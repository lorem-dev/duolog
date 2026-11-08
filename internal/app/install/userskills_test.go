package install

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/plugin"
)

// userSkill is the SKILL.md of the bundled skill name of agent at user scope
// under home, CLAUDE_CONFIG_DIR unset.
func userSkill(home, agent, name string) string {
	if agent == "claude" {
		return filepath.Join(home, ".claude", "skills", name, "SKILL.md")
	}
	return filepath.Join(home, ".agents", "skills", name, "SKILL.md")
}

// bundled is the text of a file of a bundled skill.
func bundled(t *testing.T, agent, name, rel string) string {
	t.Helper()
	data, err := plugin.FS.ReadFile(agent + "/skills/" + name + "/" + rel)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// withKnown adds the content id of text to the contents some duolog wrote,
// for the test.
func withKnown(t *testing.T, text string) {
	t.Helper()
	id := contentID([]byte(text))
	writtenBlobs[id] = true
	t.Cleanup(func() { delete(writtenBlobs, id) })
}

func TestBlobIDIsGitsObjectID(t *testing.T) {
	// git hash-object of "hello\n"; CRLF counts as LF.
	for _, text := range []string{"hello\n", "hello\r\n"} {
		if got := blobID([]byte(text)); got != "ce013625030ba8dba906f756967f9e9ca394464a" {
			t.Fatalf("%q: %s", text, got)
		}
	}
}

func TestSkillsCarryConditionFirstLineAndVersion(t *testing.T) {
	v := Version()
	if v == "" {
		t.Fatal("the duolog skill names no version")
	}
	for _, agent := range []string{"claude", "codex"} {
		for _, name := range plugin.Skills {
			path := agent + "/skills/" + name + "/SKILL.md"
			front, body := splitSkill(t, path)
			if got := SkillVersion([]byte(bundled(t, agent, name, "SKILL.md"))); got != v {
				t.Errorf("%s: version %q, want %q", path, got, v)
			}
			desc := ""
			for _, line := range strings.Split(front, "\n") {
				if rest, ok := strings.CutPrefix(line, "description:"); ok {
					desc = strings.TrimSpace(rest)
				} else if desc == ">-" {
					desc = strings.TrimSpace(line)
				}
			}
			if !strings.HasPrefix(desc, "Use only in a project that has .duolog/config.yaml") {
				t.Errorf("%s: description %q", path, desc)
			}
			first, _, _ := strings.Cut(strings.TrimLeft(body, "\n"), "\n")
			if !strings.Contains(first, "`.duolog/config.yaml`") || !strings.Contains(strings.ToLower(first), "stop") {
				t.Errorf("%s: first line %q", path, first)
			}
		}
	}
}

func TestSkillVersionReadsMetadataOnly(t *testing.T) {
	for text, want := range map[string]string{
		"---\nname: x\nmetadata:\n  duolog-version: \"1.2.3\"\n---\nbody\n": "1.2.3",
		"---\r\nname: x\r\nmetadata:\r\n  duolog-version: 1.2.3\r\n---\r\n": "1.2.3",
		"---\nname: x\nduolog-version: 1.2.3\n---\n":                        "",
		"---\nname: x\n---\nmetadata:\n  duolog-version: 1.2.3\n":           "",
		"name: x\n": "",
	} {
		if got := SkillVersion([]byte(text)); got != want {
			t.Errorf("%q: %q, want %q", text, got, want)
		}
	}
}

func TestOlderVersion(t *testing.T) {
	for _, c := range []struct {
		a, b  string
		older bool
	}{
		{"1.5.1", "1.6.0", true}, {"1.6.0", "1.6.0", false}, {"1.10.0", "1.9.0", false},
		{"v1.5.0", "1.5.1", true}, {"1.6.0-rc1", "1.6.0", false}, {"dev", "1.6.0", false}, {"1.6", "1.6.1", true},
	} {
		if got := olderVersion(c.a, c.b); got != c.older {
			t.Errorf("olderVersion(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

// Every install puts the skills at user scope for both agents, and none in
// the project.
func TestRunWritesSkillsAtUserScopeOnly(t *testing.T) {
	for _, scope := range []string{"project", "user"} {
		t.Run(scope, func(t *testing.T) {
			home, root := setup(t)
			run(t, Options{Scope: scope, Home: home, Root: root})
			for _, agent := range []string{"claude", "codex"} {
				for _, name := range plugin.Skills {
					if got := read(t, userSkill(home, agent, name)); got != bundled(t, agent, name, "SKILL.md") {
						t.Fatalf("%s %s differs", agent, name)
					}
				}
			}
			for _, dir := range []string{".claude/skills", ".agents/skills"} {
				if isThere(root, dir) {
					t.Errorf("%s written in the project", dir)
				}
			}
		})
	}
}

func TestRunUserSkillsHonourClaudeConfigDir(t *testing.T) {
	home, root := setup(t)
	custom := filepath.Join(t.TempDir(), "claude-config")
	t.Setenv("CLAUDE_CONFIG_DIR", custom)
	run(t, Options{Scope: "project", Agent: "claude", Home: home, Root: root})
	if !isThere(custom, "skills/duolog/SKILL.md") || isThere(home, ".claude/skills") {
		t.Fatal("skills not under CLAUDE_CONFIG_DIR")
	}
}

func TestRunWithoutHomeWritesNoSkills(t *testing.T) {
	_, root := setup(t)
	cwd := t.TempDir()
	t.Chdir(cwd)
	run(t, Options{Scope: "project", Root: root})
	if isThere(cwd, ".claude") || isThere(cwd, ".agents") || isThere(root, ".claude/skills") {
		t.Fatal("skills written without a home")
	}
}

func TestRunReplacesUserSkillOfAnotherVersion(t *testing.T) {
	for _, version := range []string{"1.0.0", "99.0.0"} {
		t.Run(version, func(t *testing.T) {
			home, root := setup(t)
			path := userSkill(home, "claude", "duolog")
			text := "---\nname: duolog\nmetadata:\n  duolog-version: \"" + version + "\"\n---\nold text\n"
			withKnown(t, text)
			write(t, path, text)
			r := run(t, Options{Scope: "project", Agent: "claude", Home: home, Root: root})
			if read(t, path) != bundled(t, "claude", "duolog", "SKILL.md") || !slices.Contains(r.Files, path) {
				t.Fatalf("not replaced: %v", r.Files)
			}
			if isThere(filepath.Dir(path), "SKILL.md"+backupSuffix) {
				t.Error("kept a backup of duolog's own skill")
			}
		})
	}
}

func TestRunReplacesUserSkillOfAnEarlierInstall(t *testing.T) {
	home, root := setup(t)
	old := "---\nname: duolog\n---\nwhat duolog wrote before it marked its skills\n"
	withKnown(t, old)
	path := userSkill(home, "codex", "duolog")
	write(t, path, old)
	run(t, Options{Scope: "project", Agent: "codex", Home: home, Root: root})
	if read(t, path) != bundled(t, "codex", "duolog", "SKILL.md") {
		t.Fatal("an unmarked skill duolog wrote was not replaced")
	}
}

func TestRunLeavesUserSkillItDidNotWrite(t *testing.T) {
	home, root := setup(t)
	path := userSkill(home, "claude", "duolog")
	write(t, path, "---\nname: duolog\n---\nmy own skill\n")
	r := run(t, Options{Scope: "project", Agent: "claude", Home: home, Root: root})
	if read(t, path) != "---\nname: duolog\n---\nmy own skill\n" {
		t.Fatal("overwrote a skill duolog did not write")
	}
	if !slices.ContainsFunc(r.Notes, func(n string) bool { return strings.Contains(n, filepath.Dir(path)) }) {
		t.Fatalf("no note names it: %v", r.Notes)
	}
	// The other skills are installed.
	if !isThere(home, ".claude/skills/duolog-peers/SKILL.md") {
		t.Fatal("other skills missing")
	}
	statuses, err := testDeps().Check(Options{Scope: "project", Agent: "claude", Home: home, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	s := statuses[0]
	if s.Stale() || !slices.Contains(s.Foreign, filepath.Dir(path)) || len(s.Lines(root)) != 1 {
		t.Fatalf("stale %v, foreign %v, lines %v", s.Stale(), s.Foreign, s.Lines(root))
	}
}

func TestRunLeavesLinkedUserSkill(t *testing.T) {
	home, root := setup(t)
	target := t.TempDir()
	marked := "---\nname: duolog\nmetadata:\n  duolog-version: \"1.0.0\"\n---\nchecked out\n"
	write(t, filepath.Join(target, "SKILL.md"), marked)
	dir := filepath.Dir(userSkill(home, "claude", "duolog"))
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, dir); err != nil {
		t.Skip(err)
	}
	run(t, Options{Scope: "project", Agent: "claude", Home: home, Root: root})
	if read(t, filepath.Join(target, "SKILL.md")) != marked {
		t.Fatal("wrote through a linked skill folder")
	}
}

func TestRunRemovesUnchangedProjectCopies(t *testing.T) {
	home, root := setup(t)
	old := "---\nname: duolog-peers\n---\nan older release\n"
	withKnown(t, old)
	current := filepath.Join(root, ".claude", "skills", "duolog", "SKILL.md")
	write(t, current, bundled(t, "claude", "duolog", "SKILL.md"))
	older := filepath.Join(root, ".claude", "skills", "duolog-peers", "SKILL.md")
	write(t, older, old)
	changed := filepath.Join(root, ".agents", "skills", "duolog", "SKILL.md")
	write(t, changed, bundled(t, "codex", "duolog", "SKILL.md")+"my note\n")
	foreign := filepath.Join(root, ".claude", "skills", "mine", "SKILL.md")
	write(t, foreign, "mine\n")
	r := run(t, Options{Scope: "project", Home: home, Root: root})
	if isThere(root, ".claude/skills/duolog") || isThere(root, ".claude/skills/duolog-peers") {
		t.Fatal("unchanged copies stay")
	}
	if read(t, changed) != bundled(t, "codex", "duolog", "SKILL.md")+"my note\n" || read(t, foreign) != "mine\n" {
		t.Fatal("removed a changed copy or another skill")
	}
	if !slices.ContainsFunc(r.Notes, func(n string) bool { return strings.Contains(n, filepath.Dir(changed)) }) {
		t.Fatalf("no note for the changed copy: %v", r.Notes)
	}
	statuses, err := testDeps().Check(Options{Scope: "project", Home: home, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range statuses {
		if s.Stale() {
			t.Fatalf("%s stale: %v", s.Agent, s.Changed)
		}
		if s.Agent == "codex" && !slices.Contains(s.Kept, filepath.Dir(changed)) {
			t.Fatalf("kept %v", s.Kept)
		}
	}
}

func TestCheckReportsUnchangedProjectCopy(t *testing.T) {
	home, root := setup(t)
	o := Options{Scope: "project", Agent: "claude", Home: home, Root: root}
	run(t, o)
	copyPath := filepath.Join(root, ".claude", "skills", "duolog", "SKILL.md")
	write(t, copyPath, bundled(t, "claude", "duolog", "SKILL.md"))
	statuses, err := testDeps().Check(o)
	if err != nil {
		t.Fatal(err)
	}
	if !statuses[0].Stale() || !slices.Contains(statuses[0].Changed, copyPath) {
		t.Fatalf("changed %v", statuses[0].Changed)
	}
}

func TestCheckReportsMissingAndOldUserSkill(t *testing.T) {
	home, root := setup(t)
	o := Options{Scope: "project", Agent: "claude", Home: home, Root: root}
	run(t, o)
	if err := os.RemoveAll(filepath.Dir(userSkill(home, "claude", "duolog-peers"))); err != nil {
		t.Fatal(err)
	}
	old := userSkill(home, "claude", "duolog")
	oldText := strings.Replace(read(t, old), Version(), "1.0.0", 1)
	withKnown(t, oldText)
	write(t, old, oldText)
	statuses, err := testDeps().Check(o)
	if err != nil {
		t.Fatal(err)
	}
	s := statuses[0]
	if !s.Stale() || !slices.Contains(s.MissingSkills, filepath.Dir(userSkill(home, "claude", "duolog-peers"))) || !slices.Contains(s.Changed, old) {
		t.Fatalf("missing %v, changed %v", s.MissingSkills, s.Changed)
	}
	if line := s.Line(root); !strings.Contains(line, "duolog skills missing") || !strings.Contains(line, old) {
		t.Fatalf("line %q", line)
	}
	if note := testDeps().ProjectNote(root, home); !strings.Contains(note, "missing") {
		t.Fatalf("note %q", note)
	}
}

// A user install is its hooks: the user skills that a project install wrote
// do not make one.
func TestCheckUserScopeIgnoresSkillsOfProjectInstall(t *testing.T) {
	home, root := setup(t)
	run(t, Options{Scope: "project", Home: home, Root: root})
	statuses, err := testDeps().Check(Options{Scope: "user", Home: home, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range statuses {
		if s.Agent == "codex" && (s.Installed || s.Stale()) {
			t.Fatalf("codex user install %v, changed %v", s.Installed, s.Changed)
		}
	}
}

func TestRefreshSkillsReplacesOnlyOlder(t *testing.T) {
	home, root := setup(t)
	run(t, Options{Scope: "project", Home: home, Root: root})
	older := userSkill(home, "claude", "duolog")
	olderText := strings.Replace(read(t, older), Version(), "1.0.0", 1) + "stale\n"
	withKnown(t, olderText)
	write(t, older, olderText)
	newer := userSkill(home, "codex", "duolog")
	newerText := strings.Replace(read(t, newer), Version(), "99.0.0", 1)
	write(t, newer, newerText)
	foreign := userSkill(home, "codex", "duolog-peers")
	write(t, foreign, "mine\n")
	missing := filepath.Dir(userSkill(home, "claude", "duolog-calibrate"))
	if err := os.RemoveAll(missing); err != nil {
		t.Fatal(err)
	}
	note, err := testDeps().RefreshSkills(root, home)
	if err != nil {
		t.Fatal(err)
	}
	if read(t, older) != bundled(t, "claude", "duolog", "SKILL.md") || !strings.Contains(note, Version()) {
		t.Fatalf("older skill not replaced, note %q", note)
	}
	if read(t, newer) != newerText || read(t, foreign) != "mine\n" || isThere(missing, "") {
		t.Fatal("refresh touched a newer, foreign or missing skill")
	}
	if note, _ := testDeps().RefreshSkills(root, home); note != "" {
		t.Fatalf("second refresh says %q", note)
	}
}

// A skill that duolog wrote and the person then changed is theirs now:
// install and the session-start refresh leave it with a note, Check lists
// it, and uninstall removes only the files duolog wrote unchanged.
func TestRunLeavesEditedUserSkill(t *testing.T) {
	home, root := setup(t)
	o := Options{Scope: "project", Agent: "claude", Home: home, Root: root}
	run(t, o)
	path := userSkill(home, "claude", "duolog")
	edited := strings.Replace(read(t, path), Version(), "1.0.0", 1) + "my own line\n"
	write(t, path, edited)
	r := run(t, o)
	if read(t, path) != edited || slices.Contains(r.Files, path) {
		t.Fatalf("install replaced an edited skill: %v", r.Files)
	}
	note := "Claude: left " + filepath.Dir(path) + ": it has changes duolog did not write; keep your changes elsewhere, remove the folder and run duolog install to get duolog's skill back"
	if !slices.Contains(r.Notes, note) {
		t.Fatalf("notes %q", r.Notes)
	}
	if isThere(filepath.Dir(path), "SKILL.md"+backupSuffix) {
		t.Fatal("install wrote a backup beside the skill")
	}
	if note, err := testDeps().RefreshSkills(root, home); err != nil || note != "" || read(t, path) != edited {
		t.Fatalf("refresh: note %q, err %v", note, err)
	}
	s := check(t, o)["claude"]
	if s.Stale() || !slices.Equal(s.Edited, []string{filepath.Dir(path)}) || len(s.Extra) != 0 {
		t.Fatalf("status %+v", s)
	}
	line := "claude: " + filepath.Dir(path) + " has changes to a duolog skill, so duolog leaves it; keep your changes elsewhere, remove it and run duolog install"
	if lines := s.Lines(root); !slices.Contains(lines, line) {
		t.Fatalf("lines %q", lines)
	}
	// A file the person added makes a skill theirs too.
	peers := userSkill(home, "claude", "duolog-peers")
	added := filepath.Join(filepath.Dir(peers), "notes.md")
	write(t, added, "my notes\n")
	if s := check(t, o)["claude"]; !slices.Contains(s.Edited, filepath.Dir(peers)) || slices.Contains(s.Extra, added) {
		t.Fatalf("status %+v", s)
	}
	r, err := testDeps().Uninstall(o)
	if err != nil {
		t.Fatal(err)
	}
	if read(t, path) != edited || read(t, added) != "my notes\n" || isThere(home, ".claude/skills/duolog-peers/SKILL.md") {
		t.Fatal("uninstall removed a changed file, or left an unchanged one")
	}
	if !slices.ContainsFunc(r.Notes, func(n string) bool { return strings.Contains(n, filepath.Dir(path)) }) {
		t.Fatalf("notes %q", r.Notes)
	}
}

// A link anywhere in a skill folder makes it not duolog's, whatever its
// SKILL.md names: install and refresh would write through it.
func TestRunLeavesUserSkillWithALinkInside(t *testing.T) {
	for _, rel := range []string{"SKILL.md", "deeper/SKILL.md"} {
		t.Run(rel, func(t *testing.T) {
			home, root := setup(t)
			outside := filepath.Join(t.TempDir(), "SKILL.md")
			marked := strings.Replace(bundled(t, "claude", "duolog", "SKILL.md"), Version(), "1.0.0", 1)
			withKnown(t, marked)
			write(t, outside, marked)
			dir := filepath.Dir(userSkill(home, "claude", "duolog"))
			if rel != "SKILL.md" {
				write(t, filepath.Join(dir, "SKILL.md"), marked)
			}
			link := filepath.Join(dir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, link); err != nil {
				t.Skip(err)
			}
			o := Options{Scope: "project", Agent: "claude", Home: home, Root: root}
			run(t, o)
			if _, err := testDeps().RefreshSkills(root, home); err != nil {
				t.Fatal(err)
			}
			if read(t, outside) != marked {
				t.Fatal("wrote through a link in the skill folder")
			}
			if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatal("the link is gone")
			}
			if s := check(t, o)["claude"]; !slices.Contains(s.Foreign, dir) {
				t.Fatalf("status %+v", s)
			}
			if _, err := testDeps().Uninstall(o); err != nil || read(t, outside) != marked {
				t.Fatalf("uninstall: %v", err)
			}
		})
	}
}

// Every file of the bundled skills is in writtenBlobs, so that a later
// duolog knows this one's files as duolog's and replaces them. A release
// changes only the version in them, which keeps their ids.
func TestEveryBundledSkillFileIsAKnownBlob(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		for _, name := range plugin.Skills {
			files, err := skillFiles(agent, name)
			if err != nil {
				t.Fatal(err)
			}
			for _, rel := range files {
				id := contentID([]byte(bundled(t, agent, name, rel)))
				if !writtenBlobs[id] {
					t.Errorf("add to writtenBlobs in skillblobs.go:\n\t%q: true, // %s/skills/%s/%s", id, agent, name, rel)
				}
			}
		}
	}
}

func TestUninstallRemovesUserSkillsWithNote(t *testing.T) {
	home, root := setup(t)
	o := Options{Scope: "project", Home: home, Root: root}
	run(t, o)
	foreign := userSkill(home, "codex", "duolog-peers")
	write(t, foreign, "mine\n")
	extra := filepath.Join(filepath.Dir(userSkill(home, "claude", "duolog")), "notes.md")
	write(t, extra, "my notes\n")
	r, err := testDeps().Uninstall(o)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(r.Notes, SkillsRemovedNote) {
		t.Fatalf("notes %v", r.Notes)
	}
	if isThere(home, ".claude/skills/duolog/SKILL.md") || isThere(home, ".claude/skills/duolog-peers") || isThere(home, ".agents/skills/duolog") {
		t.Fatal("user skills stay")
	}
	if read(t, foreign) != "mine\n" || read(t, extra) != "my notes\n" {
		t.Fatal("removed a file duolog did not write")
	}
	r, err = testDeps().Uninstall(o)
	if err != nil || slices.Contains(r.Notes, SkillsRemovedNote) {
		t.Fatalf("second uninstall: %v %v", r.Notes, err)
	}
}
