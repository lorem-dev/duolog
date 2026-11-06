package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/plugin"
)

func TestPeersSkillIsSameForBothAgents(t *testing.T) {
	_, claude := splitSkill(t, "claude/skills/duolog-peers/SKILL.md")
	_, codex := splitSkill(t, "codex/skills/duolog-peers/SKILL.md")
	if claude != codex {
		t.Fatalf("bodies differ:\n%s\n---\n%s", claude, codex)
	}
	if n := words(claude); n >= 200 {
		t.Errorf("body has %d words", n)
	}
	// The refusal of peer show is a guard against accidents, not a barrier.
	for _, want := range []string{"duolog peer list --long", "duolog peer view", "duolog peer show", "accidents", "on purpose"} {
		if !strings.Contains(claude, want) {
			t.Errorf("body lacks %q", want)
		}
	}
}

// The list and the view are pre-allowed: the person decided that Claude may
// switch their Rex view without asking when they ask to see a peer. peer
// show stays out: it reads a peer's screen.
func TestPeersSkillAllowsListAndView(t *testing.T) {
	front, _ := splitSkill(t, "claude/skills/duolog-peers/SKILL.md")
	var got []string
	for _, line := range strings.Split(front, "\n") {
		if v, ok := strings.CutPrefix(line, "allowed-tools:"); ok {
			got = append(got, strings.TrimSpace(v))
		}
	}
	want := "Bash(duolog peer list*), Bash(duolog peer view*)"
	if len(got) != 1 || got[0] != want {
		t.Errorf("claude allowed-tools = %q, want %q", got, want)
	}
	front, _ = splitSkill(t, "codex/skills/duolog-peers/SKILL.md")
	if strings.Contains(front, "allowed-tools") {
		t.Errorf("codex frontmatter has allowed-tools:\n%s", front)
	}
}

func TestPeersSkillIsInstalled(t *testing.T) {
	home, root := setup(t)
	o := Options{Scope: "project", Agent: "both", Root: root, Home: home}
	if _, err := Run(o); err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, agent := range []string{"claude", "codex"} {
		dir := filepath.Join(filepath.Dir(filepath.Dir(installedSkillPath(o, root, agent))), "duolog-peers")
		dirs = append(dirs, dir)
		want, err := plugin.FS.ReadFile(agent + "/skills/duolog-peers/SKILL.md")
		if err != nil {
			t.Fatal(err)
		}
		if have := read(t, filepath.Join(dir, "SKILL.md")); have != string(want) {
			t.Errorf("%s: installed skill differs", agent)
		}
	}
	if err := Remove(o); err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("%s remains: %v", dir, err)
		}
	}
}

// The skills page names every skill that duolog install ships.
func TestSkillsPageNamesEveryBundledSkill(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "skills.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range plugin.Skills {
		// The page is about the duolog skill's tools and names it in prose.
		if name == "duolog" {
			continue
		}
		if !strings.Contains(string(data), "`"+name+"`") {
			t.Errorf("docs/skills.md does not name `%s`", name)
		}
	}
}

func TestSkillsListNamesEveryBundledSkill(t *testing.T) {
	want := []string{"duolog", "duolog-skill-check", "duolog-calibrate", "duolog-peers"}
	if strings.Join(plugin.Skills, ",") != strings.Join(want, ",") {
		t.Fatalf("plugin.Skills = %v, want %v", plugin.Skills, want)
	}
}
