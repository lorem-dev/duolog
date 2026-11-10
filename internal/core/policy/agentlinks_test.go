package policy

import (
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/mode"
)

func linkedSkillHome(t *testing.T, custom bool) (readsFixture, string) {
	t.Helper()
	f := skillHome(t)
	config := ".codex"
	if custom {
		config = "agent-config"
		t.Setenv("CODEX_HOME", f.path(config))
	}
	tree := config + "/superpowers/skills"
	f.put(t, tree+"/using-superpowers/SKILL.md", tree+"/references/guide.md")
	f.link(t, ".agent/skills/superpowers", f.path(tree))
	return f, tree
}

func TestAgentLinkedSkillRead(t *testing.T) {
	// Kills retaining only the lexical install root, which loses linked trees in agent config.
	p := policy(t)
	for _, custom := range []bool{false, true} {
		f, tree := linkedSkillHome(t, custom)
		for _, name := range []string{"using-superpowers/SKILL.md", "references/guide.md"} {
			installed := ".agent/skills/superpowers/" + name
			for _, a := range []Action{
				f.exec("cat " + f.word(installed)),
				f.exec("cat ~/" + installed),
				f.exec("cat " + f.word(tree+"/"+name)),
				f.read(f.path(installed)),
			} {
				a.Role = "implement"
				for _, m := range allModes {
					if d := Evaluate(p, LeadRules{}, m, a); d.Kind != Allow {
						t.Errorf("%s %s in %s: %+v", a.Kind, a.Target, m, d)
					}
				}
			}
		}
	}
}

func TestAgentLinkedSkillKeepsPrivateFiles(t *testing.T) {
	// Kills bypassing privateSkillFiles or hidden-name checks for a linked skill root.
	p := policy(t)
	f, tree := linkedSkillHome(t, false)
	for _, name := range append([]string{".hidden", "nested/.hidden/guide.md"}, privateSkillFiles...) {
		f.put(t, tree+"/"+name)
		for _, a := range []Action{f.exec("cat " + f.word(".agent/skills/superpowers/"+name)), f.read(f.path(tree + "/" + name))} {
			if d := Evaluate(p, LeadRules{}, mode.Full, a); d.Kind == Allow {
				t.Errorf("private %s: %+v", name, d)
			}
		}
	}
	f.link(t, tree+"/guide.md", f.path(tree+"/auth.json"))
	if d := Evaluate(p, LeadRules{}, mode.Full, f.exec("cat "+f.word(".agent/skills/superpowers/guide.md"))); d.Kind == Allow {
		t.Fatalf("credential alias: %+v", d)
	}
}

func TestAgentLinkedSkillKeepsResolvedBoundary(t *testing.T) {
	// Kills granting the tree's config parent or following a file link out of the tree.
	p := policy(t)
	f, tree := linkedSkillHome(t, false)
	parent := filepath.ToSlash(filepath.Dir(tree))
	f.put(t, parent+"/notes.md", ".codex/auth.json")
	f.link(t, tree+"/escape.md", f.path(parent+"/notes.md"))
	f.link(t, ".agent/skills/config", f.path(".codex"))
	for _, name := range []string{
		".agent/skills/superpowers/escape.md", parent + "/notes.md",
		".agent/skills/config/auth.json", ".agent/skills/config/superpowers/notes.md",
	} {
		for _, a := range []Action{f.exec("cat " + f.word(name)), f.read(f.path(name))} {
			if d := Evaluate(p, LeadRules{}, mode.Full, a); d.Kind == Allow {
				t.Errorf("outside tree %s: %+v", name, d)
			}
		}
	}
}
