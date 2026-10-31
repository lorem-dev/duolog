package agentsync

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
)

const skillMD = "---\nname: probe\ndescription: Probes things.\n---\n# Probe\n\nBody stays   exactly\n\tas it is.\n"

func skillProject(t *testing.T) string {
	root := newProject(t)
	put(t, root, ".claude/skills/probe/SKILL.md", skillMD, 0o644)
	put(t, root, ".claude/skills/probe/scripts/a.sh", "#!/bin/sh\necho a\n", 0o755)
	put(t, root, ".claude/skills/probe/references/b.md", "# B\n", 0o644)
	return root
}

func TestSkillCopiedWhole(t *testing.T) {
	root := skillProject(t)
	if _, err := Run(root, syncCfg(t), Options{From: "claude"}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"SKILL.md", "scripts/a.sh", "references/b.md"} {
		if _, err := os.Stat(filepath.Join(root, ".agents/skills/probe", rel)); err != nil {
			t.Fatal(err)
		}
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(filepath.Join(root, ".agents/skills/probe/scripts/a.sh"))
		if info.Mode().Perm()&0o100 == 0 {
			t.Fatalf("exec bit lost: %v", info.Mode())
		}
	}
	if got := read(t, root, ".agents/skills/probe/references/b.md"); got != "# B\n" {
		t.Fatalf("got %q", got)
	}
}

func TestNoSymlinksCreated(t *testing.T) {
	root := skillProject(t)
	if _, err := Run(root, syncCfg(t), Options{From: "claude"}); err != nil {
		t.Fatal(err)
	}
	count := 0
	err := filepath.WalkDir(filepath.Join(root, ".agents"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		count++
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			t.Errorf("%s is not a regular file: %v", path, info.Mode())
		}
		return nil
	})
	if err != nil || count != 3 {
		t.Fatalf("count %d err %v", count, err)
	}
}

func TestSkillBodyUnchanged(t *testing.T) {
	root := skillProject(t)
	if _, err := Run(root, syncCfg(t), Options{From: "claude"}); err != nil {
		t.Fatal(err)
	}
	got := read(t, root, ".agents/skills/probe/SKILL.md")
	_, body, err := splitFrontmatter([]byte(got))
	if err != nil {
		t.Fatal(err)
	}
	_, want, _ := splitFrontmatter([]byte(skillMD))
	if body != want {
		t.Fatalf("body changed: %q", body)
	}
	if !strings.Contains(got, generatedComment) {
		t.Fatalf("no generated comment: %q", got)
	}
}

func TestSkillDropKeys(t *testing.T) {
	root := newProject(t)
	put(t, root, ".claude/skills/probe/SKILL.md", "---\nname: probe\ndescription: d\nallowed-tools: Bash\n---\nbody\n", 0o644)
	cfg := syncCfg(t)
	cfg.Skills = config.SkillSync{DropKeys: []string{"allowed-tools"}}
	if _, err := Run(root, cfg, Options{From: "claude"}); err != nil {
		t.Fatal(err)
	}
	got := read(t, root, ".agents/skills/probe/SKILL.md")
	if strings.Contains(got, "allowed-tools") || !strings.Contains(got, "name: probe") {
		t.Fatalf("got %q", got)
	}
}

func TestSkillUnknownKeyKeptSilently(t *testing.T) {
	root := newProject(t)
	put(t, root, ".claude/skills/probe/SKILL.md", "---\nname: probe\ndescription: d\nmystery: 1\n---\nbody\n", 0o644)
	rep, err := Run(root, syncCfg(t), Options{From: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, root, ".agents/skills/probe/SKILL.md"), "mystery: 1") {
		t.Fatal("unknown key dropped")
	}
	if len(rep.Warnings) != 0 || len(rep.Conflicts) != 0 {
		t.Fatalf("%+v", rep)
	}
}

func TestSkillDisableModelInvocationWarnsOnce(t *testing.T) {
	root := newProject(t)
	put(t, root, ".claude/skills/probe/SKILL.md", "---\nname: probe\ndescription: d\ndisable-model-invocation: true\n---\nbody\n", 0o644)
	rep, err := Run(root, syncCfg(t), Options{From: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], "disable-model-invocation") {
		t.Fatalf("%+v", rep.Warnings)
	}
	if !strings.Contains(read(t, root, ".agents/skills/probe/SKILL.md"), "disable-model-invocation: true") {
		t.Fatal("key must stay")
	}
}

func TestSkillFileRemovedFromSource(t *testing.T) {
	root := skillProject(t)
	if _, err := Run(root, syncCfg(t), Options{From: "claude"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, ".claude/skills/probe/references/b.md")); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(root, syncCfg(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".agents/skills/probe/references/b.md")); !os.IsNotExist(err) {
		t.Fatalf("still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".agents/skills/probe/references")); !os.IsNotExist(err) {
		t.Fatalf("empty folder kept: %v", err)
	}
	if len(rep.Removed) != 1 {
		t.Fatalf("%+v", rep)
	}
}
