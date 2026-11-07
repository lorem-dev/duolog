package main

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/testbin"
)

// installedProject installs duolog for both agents in a new project folder,
// with a temporary home, and makes it the working directory.
func installedProject(t *testing.T) (root, home string) {
	t.Helper()
	isolatedRouteConfig(t)
	root = t.TempDir()
	t.Chdir(root)
	if out, err := executeBusCommand(t, "install", "--scope", "project"); err != nil {
		t.Fatal(out, err)
	}
	return root, os.Getenv("HOME")
}

func TestInstallCheckCurrentExitsZero(t *testing.T) {
	root, home := installedProject(t)
	before, homeBefore := treeState(t, root), treeState(t, home)
	out, err := executeBusCommand(t, "install", "--check")
	if err != nil {
		t.Fatal(out, err)
	}
	for _, want := range []string{"claude project install is current", "codex project install is current"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q missing %q", out, want)
		}
	}
	if !maps.Equal(before, treeState(t, root)) || !maps.Equal(homeBefore, treeState(t, home)) {
		t.Fatal("install --check wrote a file")
	}
}

func TestInstallCheckStaleExitsOne(t *testing.T) {
	root, home := installedProject(t)
	skill := filepath.Join(root, ".claude", "skills", "duolog", "SKILL.md")
	if err := os.WriteFile(skill, []byte("an older skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, homeBefore := treeState(t, root), treeState(t, home)
	out, err := executeBusCommand(t, "install", "--check", "--scope", "project")
	if err == nil || exitCode(err) != 1 {
		t.Fatalf("err %v, output %q", err, out)
	}
	want := "claude project install is out of date: " + filepath.Join(".claude", "skills", "duolog", "SKILL.md") + "; run duolog install --scope project --agent claude"
	if !strings.Contains(out, want+"\n") || !strings.Contains(out, "codex project install is current") {
		t.Fatalf("output %q missing %q", out, want)
	}
	if !maps.Equal(before, treeState(t, root)) || !maps.Equal(homeBefore, treeState(t, home)) {
		t.Fatal("install --check wrote a file")
	}
	// --agent narrows the check like it narrows install.
	if out, err := executeBusCommand(t, "install", "--check", "--agent", "codex"); err != nil || strings.Contains(out, "claude") {
		t.Fatalf("err %v, output %q", err, out)
	}
}

// A clone of a repository that commits duolog's hooks and ignores its skill
// folders has the hooks and no skills.
func TestInstallCheckCommittedHooksWithoutSkills(t *testing.T) {
	root, _ := installedProject(t)
	for _, dir := range []string{filepath.Join(root, ".claude", "skills"), filepath.Join(root, ".agents", "skills")} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	out, err := executeBusCommand(t, "install", "--check")
	if err == nil || exitCode(err) != 1 {
		t.Fatalf("err %v, output %q", err, out)
	}
	for _, want := range []string{"claude project install is out of date: duolog skills missing: " + filepath.Join(".claude", "skills", "duolog") + ", ", "codex project install is out of date: duolog skills missing: " + filepath.Join(".agents", "skills", "duolog") + ", ", "; run duolog install --scope project --agent codex\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q missing %q", out, want)
		}
	}
}

// With agents.codex.skills_dir set, the check compares the folder the key
// selects, and a file left in duolog's skill folders in .agents/skills counts
// as install treats it: install removes every one of them, whatever its
// content, and leaves other skills alone.
func TestInstallCheckHonoursSkillsDir(t *testing.T) {
	root := skillsDirProject(t)
	if out, err := executeBusCommand(t, "install", "--scope", "project", "--agent", "codex"); err != nil {
		t.Fatal(out, err)
	}
	out, err := executeBusCommand(t, "install", "--check", "--agent", "codex")
	if err != nil || !strings.Contains(out, "codex project install is current") {
		t.Fatalf("err %v, output %q", err, out)
	}
	// An earlier install with the default folder left duolog's skill there.
	skill, err := os.ReadFile(filepath.Join(root, ".codex", "skills", "duolog", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	leftover := filepath.Join(root, ".agents", "skills", "duolog", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(leftover), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leftover, skill, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = executeBusCommand(t, "install", "--check", "--agent", "codex")
	want := "codex project install is out of date: " + filepath.Join(".agents", "skills", "duolog", "SKILL.md") + "; run duolog install --scope project --agent codex\n"
	if err == nil || !strings.Contains(out, want) {
		t.Fatalf("err %v, output %q, want %q", err, out, want)
	}
	if out, err := executeBusCommand(t, "install", "--scope", "project", "--agent", "codex"); err != nil {
		t.Fatal(out, err)
	}
	if out, err := executeBusCommand(t, "install", "--check", "--agent", "codex"); err != nil {
		t.Fatalf("after install: %v %q", err, out)
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Fatalf("install left %s: %v", leftover, err)
	}
	// A changed file in a duolog skill folder there goes too, so it is stale;
	// a skill of another name there is the person's.
	for path, data := range map[string]string{
		filepath.Join(root, ".agents", "skills", "duolog-peers", "notes.md"): "an older file\n",
		filepath.Join(root, ".agents", "skills", "mine", "SKILL.md"):         "mine\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err = executeBusCommand(t, "install", "--check", "--agent", "codex")
	want = "codex project install is out of date: " + filepath.Join(".agents", "skills", "duolog-peers", "notes.md") + "; run duolog install --scope project --agent codex\n"
	if err == nil || !strings.Contains(out, want) {
		t.Fatalf("err %v, output %q, want %q", err, out, want)
	}
	if out, err := executeBusCommand(t, "install", "--scope", "project", "--agent", "codex"); err != nil {
		t.Fatal(out, err)
	}
	if out, err := executeBusCommand(t, "install", "--check", "--agent", "codex"); err != nil {
		t.Fatalf("after install: %v %q", err, out)
	}
	// Doctor reads the key the same way.
	codex := testbin.Install(t, "codex", testbin.Scenario{Stdout: "{}"})
	claude := testbin.Install(t, "claude", testbin.Scenario{})
	t.Setenv("PATH", filepath.Dir(codex)+string(os.PathListSeparator)+filepath.Dir(claude))
	fakeRexFirst(t)
	if out, err := executeBusCommand(t, "doctor"); err != nil || strings.Contains(out, "install is out of date") {
		t.Fatalf("doctor: %v %q", err, out)
	}
}

// When the configured folder is a link to .agents/skills, both names are one
// folder: install keeps the skills there, and the check finds no leftover.
func TestInstallCheckSkillsDirLinkedToDefault(t *testing.T) {
	root := skillsDirProject(t)
	if err := os.MkdirAll(filepath.Join(root, ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", ".agents", "skills"), filepath.Join(root, ".codex", "skills")); err != nil {
		t.Skip("no symbolic links here:", err)
	}
	if out, err := executeBusCommand(t, "install", "--scope", "project", "--agent", "codex"); err != nil {
		t.Fatal(out, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".agents", "skills", "duolog", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	out, err := executeBusCommand(t, "install", "--check", "--agent", "codex")
	if err != nil || !strings.Contains(out, "codex project install is current") {
		t.Fatalf("err %v, output %q", err, out)
	}
}

// A duolog skill in .agents/skills behind a link, here .agents/skills itself,
// is one install leaves alone, since the link may lead out of the project;
// the check does not count it either.
func TestInstallCheckSkipsLinkedOldSkill(t *testing.T) {
	root := skillsDirProject(t)
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "duolog-peers"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "duolog-peers", "SKILL.md"), []byte("an older skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".agents", "skills")); err != nil {
		t.Skip("no symbolic links here:", err)
	}
	out, err := executeBusCommand(t, "install", "--scope", "project", "--agent", "codex")
	if err != nil || !strings.Contains(out, "is a link") {
		t.Fatalf("err %v, output %q", err, out)
	}
	out, err = executeBusCommand(t, "install", "--check", "--agent", "codex")
	if err != nil || !strings.Contains(out, "codex project install is current") {
		t.Fatalf("err %v, output %q", err, out)
	}
	if _, err := os.Stat(filepath.Join(outside, "duolog-peers", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

// A file that duolog does not ship in its skill folder is for the person to
// remove; install would not, so --check still passes.
func TestInstallCheckExtraFileExitsZero(t *testing.T) {
	root, _ := installedProject(t)
	if err := os.WriteFile(filepath.Join(root, ".claude", "skills", "duolog", "notes.md"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := executeBusCommand(t, "install", "--check")
	want := "claude project install: remove " + filepath.Join(".claude", "skills", "duolog", "notes.md") + ", which duolog does not ship\n"
	if err != nil || !strings.Contains(out, want) || !strings.Contains(out, "claude project install is current") {
		t.Fatalf("err %v, output %q", err, out)
	}
}

func TestInstallCheckCouldNotCheck(t *testing.T) {
	root, _ := installedProject(t)
	if err := os.WriteFile(filepath.Join(root, ".codex", "hooks.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := executeBusCommand(t, "install", "--check")
	if err == nil || exitCode(err) != 1 || err.Error() != "the duolog install could not be checked" || !strings.Contains(out, "codex project install could not be checked: ") {
		t.Fatalf("err %v, output %q", err, out)
	}
}

// A CI job of a repository that commits the hooks and ignores the skills.
func TestInstallCheckHooksOnly(t *testing.T) {
	root, _ := installedProject(t)
	for _, dir := range []string{filepath.Join(root, ".claude", "skills"), filepath.Join(root, ".agents", "skills")} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := executeBusCommand(t, "install", "--check"); err == nil {
		t.Fatal("missing skills passed the full check")
	}
	if out, err := executeBusCommand(t, "install", "--check", "--hooks-only"); err != nil || !strings.Contains(out, "codex project install is current") {
		t.Fatalf("err %v, output %q", err, out)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "settings.json"), []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"duolog","args":["hook","Stop","--agent","claude"]}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := executeBusCommand(t, "install", "--check", "--hooks-only")
	if err == nil || exitCode(err) != 1 || !strings.Contains(out, "claude project install is out of date: "+filepath.Join(".claude", "settings.json")+"; run") {
		t.Fatalf("err %v, output %q", err, out)
	}
	if _, err := executeBusCommand(t, "install", "--hooks-only"); err == nil {
		t.Fatal("--hooks-only without --check ran an install")
	}
}

func TestInstallCheckNotInstalledExitsZero(t *testing.T) {
	isolatedRouteConfig(t)
	root := t.TempDir()
	t.Chdir(root)
	out, err := executeBusCommand(t, "install", "--check")
	if err != nil || !strings.Contains(out, "claude project install: not installed") {
		t.Fatalf("err %v, output %q", err, out)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("install --check wrote %v", entries)
	}
}

func TestDoctorNamesStaleInstall(t *testing.T) {
	root, _ := installedProject(t)
	settings := filepath.Join(root, ".codex", "hooks.json")
	if err := os.WriteFile(settings, []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"duolog hook Stop --agent codex"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	codex := testbin.Install(t, "codex", testbin.Scenario{Stdout: "{}"})
	claude := testbin.Install(t, "claude", testbin.Scenario{})
	t.Setenv("PATH", filepath.Dir(codex)+string(os.PathListSeparator)+filepath.Dir(claude))
	fakeRexFirst(t)
	out, err := executeBusCommand(t, "doctor")
	if err != nil {
		t.Fatal(out, err)
	}
	want := "warning: codex project install is out of date: " + filepath.Join(".codex", "hooks.json") + "; run duolog install --scope project --agent codex\n"
	if !strings.Contains(out, want) || strings.Contains(out, "claude project install is out of date") {
		t.Fatalf("doctor output %q missing %q", out, want)
	}
}
