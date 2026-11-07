package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillsDirInstaller(t *testing.T) {
	base, home := filepath.FromSlash("/base"), filepath.FromSlash("/home")
	var def Installer
	if got, want := def.SkillDir("project", base, home), filepath.Join(base, ".agents", "skills"); got != want {
		t.Errorf("default SkillDir = %q, want %q", got, want)
	}
	if got := def.DefaultSkillDir("project", base, home); got != "" {
		t.Errorf("default DefaultSkillDir = %q, want none", got)
	}
	set := def.WithSkillsDir(".codex/skills")
	if got, want := set.SkillDir("project", base, home), filepath.Join(base, ".codex", "skills"); got != want {
		t.Errorf("configured SkillDir = %q, want %q", got, want)
	}
	if got, want := set.DefaultSkillDir("project", base, home), filepath.Join(base, ".agents", "skills"); got != want {
		t.Errorf("configured DefaultSkillDir = %q, want %q", got, want)
	}
	// User scope keeps ~/.agents/skills.
	if got, want := set.SkillDir("user", home, home), filepath.Join(home, ".agents", "skills"); got != want {
		t.Errorf("user SkillDir = %q, want %q", got, want)
	}
	if got := set.DefaultSkillDir("user", home, home); got != "" {
		t.Errorf("user DefaultSkillDir = %q, want none", got)
	}
}

func TestSkillsDirFormat(t *testing.T) {
	if got := (Format{}).Dirs().Skills; got != ".agents/skills" {
		t.Errorf("default Dirs().Skills = %q", got)
	}
	f, ok := (Format{}).WithSkillsDir(".codex/skills").(Format)
	if !ok || f.Dirs().Skills != ".codex/skills" || f.Dirs().Agents != ".codex/agents" {
		t.Errorf("configured Dirs() = %+v", f.Dirs())
	}
}

// The project line follows the key. During a move the default folder is read
// too, and a name in the configured folder wins.
func TestSkillsDirCaps(t *testing.T) {
	skill := func(root, dir, name, body string) {
		putFile(t, filepath.Join(root, filepath.FromSlash(dir), name, "SKILL.md"), body)
	}

	c, home, root := capsEnv(t)
	capsPath(t, pluginList(t, `{"installed":[]}`))
	skill(home, ".agents/skills", "from-home", "x")
	skill(root, ".agents/skills", "old-only", "x")
	skill(root, ".agents/skills", "both", "old")
	skill(root, "tools/skills", "both", "new")
	skill(root, "tools/skills", "new-only", "x")

	inv, err := c.WithSkillsDir("tools/skills").Scan(context.Background(), root, os.Environ())
	if err != nil || inv.Unknown {
		t.Fatal(inv, err)
	}
	var sources []string
	for _, it := range inv.Items {
		if it.Name == "both" {
			sources = append(sources, filepath.ToSlash(it.Source))
		}
	}
	if len(sources) != 1 || !strings.HasSuffix(sources[0], "tools/skills/both/SKILL.md") {
		t.Errorf("both listed from %v, want the configured folder only", sources)
	}
	got := strings.Join(itemNames(inv), " ")
	for _, want := range []string{"skill:from-home", "skill:old-only", "skill:new-only"} {
		if !strings.Contains(got, want) {
			t.Errorf("items = %s, missing %s", got, want)
		}
	}

	// Without the key, the folder named by the key is not read.
	inv, err = c.Scan(context.Background(), root, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(itemNames(inv), " "); strings.Contains(got, "new-only") {
		t.Errorf("default scan read tools/skills: %s", got)
	}
}
