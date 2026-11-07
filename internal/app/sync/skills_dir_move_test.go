package agentsync

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
)

func skipWithoutLinks(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("links need a privilege on Windows")
	}
}

// A key change together with --switch-direction must not delete the skills a
// person wrote: the new Codex folder is empty, so the lock's sources point at
// the old one, and the run refuses instead of reading that as deleted sources.
func TestSkillsDirChangeWithSwitchDirectionKeepsSkills(t *testing.T) {
	root := skillProject(t)
	runIn(t, root, realFormats(), Options{From: "claude"})
	rep, err := Run(root, codexFolder(".codex/skills"), syncCfg(t), Options{From: "codex", SwitchDirection: true, Resolver: testModels})
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".claude/skills/probe/SKILL.md", ".claude/skills/probe/scripts/a.sh", ".claude/skills/probe/references/b.md"} {
		if !exists(root, rel) {
			t.Errorf("%s was deleted", rel)
		}
	}
	if len(rep.Removed) != 0 {
		t.Errorf("removed %v", rep.Removed)
	}
	if len(rep.Conflicts) == 0 || !strings.Contains(strings.Join(rep.Conflicts, "\n"), ".agents/skills") || !strings.Contains(strings.Join(rep.Conflicts, "\n"), ".codex/skills") {
		t.Errorf("conflicts %q do not name both folders", rep.Conflicts)
	}
}

// Codex is the source and the key changes before the skills move: the next
// run, and the auto-sync at a session start, report it and delete nothing.
func TestSkillsDirChangeWithCodexSourceKeepsGeneratedCopies(t *testing.T) {
	root := newProject(t)
	put(t, root, ".agents/skills/probe/SKILL.md", skillMD, 0o644)
	runIn(t, root, realFormats(), Options{From: "codex"})
	if !exists(root, ".claude/skills/probe/SKILL.md") {
		t.Fatal("first run did not generate the Claude copy")
	}
	formats := codexFolder(".codex/skills")
	rep := runIn(t, root, formats, Options{})
	if !exists(root, ".claude/skills/probe/SKILL.md") || len(rep.Removed) != 0 {
		t.Errorf("generated copy removed: %+v", rep)
	}
	if len(rep.Conflicts) == 0 || !strings.Contains(strings.Join(rep.Conflicts, "\n"), "move the skills from .agents/skills to .codex/skills, do not delete them") {
		t.Errorf("conflicts %q do not tell the person to move the skills and keep them", rep.Conflicts)
	}
	if line := startWithFormats(root, formats, syncCfg(t)); !strings.Contains(line, "move the skills") {
		t.Errorf("session start said %q", line)
	}
	if !exists(root, ".claude/skills/probe/SKILL.md") {
		t.Error("session start removed the generated copy")
	}
	// Once the person moves the skills, the next run follows them.
	if err := os.MkdirAll(filepath.Join(root, ".codex/skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, ".agents/skills/probe"), filepath.Join(root, ".codex/skills/probe")); err != nil {
		t.Fatal(err)
	}
	rep = runIn(t, root, formats, Options{})
	if len(rep.Conflicts) != 0 || !exists(root, ".claude/skills/probe/SKILL.md") {
		t.Errorf("after the move: %+v", rep)
	}
}

func startWithFormats(root string, formats Formats, cfg config.Sync) string {
	return SessionStart(root, formats, cfg, testModels)
}

// The configured folder is the same directory as the default one, through a
// link: the run keeps the files it wrote. It does not write through the link,
// so it says so.
func TestSkillsDirLinkedFolderKeepsFiles(t *testing.T) {
	skipWithoutLinks(t)
	root := skillProject(t)
	runIn(t, root, realFormats(), Options{From: "claude"})
	if err := os.MkdirAll(filepath.Join(root, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", ".agents", "skills"), filepath.Join(root, ".codex", "skills")); err != nil {
		t.Skip(err)
	}
	rep := runIn(t, root, codexFolder(".codex/skills"), Options{})
	if !exists(root, ".agents/skills/probe/SKILL.md") || !exists(root, ".codex/skills/probe/SKILL.md") {
		t.Errorf("file lost: %+v", rep)
	}
	if len(rep.Removed) != 0 || !strings.Contains(strings.Join(rep.Conflicts, "\n"), "outside the generated folders") {
		t.Errorf("report = %+v", rep)
	}
}

// On a file system that ignores case, .Agents/skills is the default folder.
func TestSkillsDirCaseOnlyChangeKeepsFiles(t *testing.T) {
	root := skillProject(t)
	runIn(t, root, realFormats(), Options{From: "claude"})
	if _, err := os.Stat(filepath.Join(root, ".AGENTS")); err != nil {
		t.Skip("this file system respects case")
	}
	rep := runIn(t, root, codexFolder(".Agents/skills"), Options{})
	if !exists(root, ".agents/skills/probe/SKILL.md") {
		t.Errorf("file lost: %+v", rep)
	}
	if len(rep.Removed) != 0 || len(rep.Conflicts) != 0 {
		t.Errorf("report = %+v", rep)
	}
}

// A key change with the skills moved by hand and a dry run: the old files are
// listed as removed, the new ones as written, and nothing changes on disk.
func TestSkillsDirChangeDryRun(t *testing.T) {
	root := skillProject(t)
	runIn(t, root, realFormats(), Options{From: "claude"})
	rep := runIn(t, root, codexFolder(".codex/skills"), Options{DryRun: true})
	if !slices.Contains(rep.Written, ".codex/skills/probe/SKILL.md") || !slices.Contains(rep.Removed, ".agents/skills/probe/SKILL.md") {
		t.Errorf("report = %+v", rep)
	}
	if !exists(root, ".agents/skills/probe/SKILL.md") || exists(root, ".codex") {
		t.Error("dry run changed files")
	}
}
