package policy

import (
	"os"
	"path/filepath"
	"testing"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
)

// wideSkillRoot is the width check the gate hands this package.
func wideSkillRoot(path string) bool { return configfile.RefuseRead(path) != "" }

// skillHome is a readsFixture whose outside folder is a temporary home.
func skillHome(t *testing.T) readsFixture {
	t.Helper()
	home := pathutil.Resolved(t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return readsFixture{work: pathutil.Resolved(t.TempDir()), out: home}
}

func (f readsFixture) put(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := os.MkdirAll(filepath.Dir(f.path(name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.path(name), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func (f readsFixture) link(t *testing.T, name, to string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.path(name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(to, f.path(name)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

// A skill root that a person linked to a wide folder is that folder, and
// grants nothing, by either spelling of the path.
func TestAgentSkillRootLinkedToAWideFolderGrantsNothing(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct {
		name, link, to string
		files          []string
		through        []string
	}{
		{"claude skills to its config", ".claude/skills", ".claude", []string{".claude/history.jsonl", ".claude/.credentials.json"}, []string{".claude/skills/history.jsonl", ".claude/skills/.credentials.json", ".claude/history.jsonl"}},
		{"codex skills to home", ".codex/skills", "", []string{"notes/plan.md", ".ssh/id_rsa", ".codex/auth.json"}, []string{".codex/skills/notes/plan.md", ".codex/skills/.ssh/id_rsa", ".codex/skills/.codex/auth.json"}},
		{"agents skills to home", ".agents/skills", "", []string{"notes/plan.md"}, []string{".agents/skills/notes/plan.md"}},
		{"plugin cache to home", ".codex/plugins/cache", "", []string{"a/b/c/skills/x/SKILL.md"}, []string{".codex/plugins/cache/a/b/c/skills/x/SKILL.md"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := skillHome(t)
			f.put(t, tc.files...)
			f.link(t, tc.link, f.path(tc.to))
			for _, name := range tc.through {
				for _, a := range []Action{f.read(f.path(name)), f.exec("cat " + f.word(name))} {
					for _, m := range allModes {
						if d := Evaluate(p, LeadRules{RoleSandbox: "read-only"}, m, a); d.Kind == Allow {
							t.Errorf("%s %s in %s: %+v", a.Kind, name, m, d)
						}
					}
				}
			}
		})
	}
	t.Run("link to a narrow folder keeps working", func(t *testing.T) {
		f := skillHome(t)
		f.put(t, "dotfiles/skills/x/SKILL.md")
		f.link(t, ".claude/skills", f.path("dotfiles/skills"))
		a := f.read(f.path(".claude/skills/x/SKILL.md"))
		if d := Evaluate(p, LeadRules{RoleSandbox: "read-only"}, mode.Ask, a); d.Kind != Allow {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("no width check grants nothing", func(t *testing.T) {
		f := skillHome(t)
		f.put(t, ".agents/skills/x/SKILL.md")
		a := f.read(f.path(".agents/skills/x/SKILL.md"))
		a.WideRead = nil
		if d := Evaluate(p, LeadRules{RoleSandbox: "read-only"}, mode.Ask, a); d.Kind == Allow {
			t.Fatalf("%+v", d)
		}
	})
}

// A skill tree holds instructions. A hidden file or a credential or config
// file in it still goes to the person; what a skill ships does not.
func TestAgentSkillTreeKeepsSecretsFromWorkers(t *testing.T) {
	p := policy(t)
	for _, tree := range []string{".agents/skills/x", ".claude/skills/x", ".codex/plugins/cache/vendor/plugin/1/skills/x"} {
		t.Run(tree, func(t *testing.T) {
			for _, name := range []string{".netrc", "auth.json", "AUTH.JSON", ".mcp.json", "mcp.json", "credentials.json", "settings.json", "config.toml", "nested/.hidden/notes.md", "nested/auth.json"} {
				f := skillHome(t)
				f.put(t, tree+"/"+name, tree+"/SKILL.md")
				for _, a := range []Action{f.read(f.path(tree + "/" + name)), f.exec("cat " + f.word(tree+"/"+name))} {
					for _, m := range allModes {
						if d := Evaluate(p, LeadRules{RoleSandbox: "read-only"}, m, a); d.Kind == Allow {
							t.Errorf("%s %s in %s: %+v", a.Kind, name, m, d)
						}
					}
				}
				if d := Evaluate(p, LeadRules{RoleSandbox: "read-only"}, mode.Ask, f.read(f.path(tree+"/"+name))); d.Kind != Ask {
					t.Errorf("%s: %+v, want an ask", name, d)
				}
				for _, target := range []string{tree, tree + "/*"} {
					if d := Evaluate(p, LeadRules{RoleSandbox: "read-only"}, mode.Ask, f.read(f.path(target))); d.Kind == Allow {
						t.Errorf("%s beside %s: %+v", target, name, d)
					}
				}
			}
			f := skillHome(t)
			shipped := []string{"SKILL.md", "scripts/run.sh", "references/guide.md", "agents/openai.yaml", "package.json"}
			for _, name := range shipped {
				f.put(t, tree+"/"+name)
			}
			for _, target := range append(shipped, ".") {
				path := filepath.Join(f.path(tree), target)
				if d := Evaluate(p, LeadRules{RoleSandbox: "read-only"}, mode.Ask, f.read(path)); d.Kind != Allow {
					t.Errorf("%s: %+v", target, d)
				}
			}
		})
	}
	t.Run("a link inside the tree to a hidden file", func(t *testing.T) {
		f := skillHome(t)
		f.put(t, ".agents/skills/x/.netrc")
		f.link(t, ".agents/skills/x/notes.md", f.path(".agents/skills/x/.netrc"))
		if d := Evaluate(p, LeadRules{RoleSandbox: "read-only"}, mode.Ask, f.read(f.path(".agents/skills/x/notes.md"))); d.Kind == Allow {
			t.Fatalf("%+v", d)
		}
	})
}

func TestAgentSkillReads(t *testing.T) {
	p := policy(t)
	home, _ := os.UserHomeDir()
	f := readsFixture{work: pathutil.Resolved(t.TempDir()), out: home}
	for _, dir := range []string{
		".agent/skills", ".agents/skills", ".codex/skills", ".claude/skills",
		".codex/plugins/cache/vendor/plugin/1/skills",
		".claude/plugins/cache/vendor/plugin/1/skills",
	} {
		file := f.path(dir + "/example/SKILL.md")
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("instructions\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, a := range []Action{
			f.read(file), f.read(filepath.Dir(file)),
			f.exec("cat " + filepath.ToSlash(file)),
			f.exec("cat ~/" + dir + "/example/SKILL.md"),
			f.exec("sed -n '1,20p' " + filepath.ToSlash(file)),
			f.exec("cat " + filepath.ToSlash(file) + " | head -20"),
		} {
			for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Edits, mode.Full} {
				if d := Evaluate(p, LeadRules{RoleSandbox: "read-only"}, m, a); d.Kind != Allow {
					t.Errorf("%s %s in %s: %+v", a.Kind, a.Target, m, d)
				}
			}
		}
	}
}

func TestAgentSkillReadsKeepBoundaries(t *testing.T) {
	p := policy(t)
	home, _ := os.UserHomeDir()
	f := readsFixture{work: pathutil.Resolved(t.TempDir()), out: home}
	for _, name := range []string{
		".agent/skills/example/SKILL.md", ".agent/skills/example/.env",
		".agent/skills-copy/example/SKILL.md", ".codex/auth.json",
		".codex/plugins/cache/vendor/plugin/1/config.json", ".ssh/config",
	} {
		if err := os.MkdirAll(filepath.Dir(f.path(name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.path(name), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	file := f.word(".agent/skills/example/SKILL.md")
	for _, a := range []Action{
		f.read(f.path(".agent/skills-copy/example/SKILL.md")),
		f.read(f.path(".codex/auth.json")),
		f.read(f.path(".codex/plugins/cache/vendor/plugin/1/config.json")),
		f.read(f.path(".agent/skills/example/.env")),
		f.read(f.path(".agent/skills/example")), // Recursive reads must respect the .env deny.
		f.exec("cat " + file + " " + f.word(".ssh/config")),
		f.exec("cat " + file + " > " + file),
		f.exec("sed -i 's/x/y/' " + file),
		{Kind: "write", Target: f.path(".agent/skills/example/SKILL.md"), Role: "implement", Work: f.work},
	} {
		a.Role = "implement"
		if d := Evaluate(p, LeadRules{}, mode.Full, a); d.Kind == Allow {
			t.Errorf("%s %s: %+v", a.Kind, a.Target, d)
		}
	}
	a := f.read(f.path(".agent/skills/example/SKILL.md"))
	deny := config.Rule{Action: "read", Match: "**/SKILL.md", Decision: Deny}
	if d := Evaluate(p, LeadRules{Deny: []config.Rule{deny}}, mode.Full, a); d.Kind != Deny {
		t.Errorf("lead deny: %+v", d)
	}
	p.Rules = append(p.Rules, deny)
	if d := Evaluate(p, LeadRules{}, mode.Full, a); d.Kind != Deny {
		t.Errorf("policy deny: %+v", d)
	}
	a = f.read(f.path(".agent/skills/example/SKILL.md"))
	a.ProtectedRoots = []string{filepath.Dir(f.path(".agent/skills/example/SKILL.md"))}
	if d := Evaluate(p, LeadRules{}, mode.Full, a); d.Kind != Deny || d.Rule != RuleProtected {
		t.Errorf("protected skill folder: %+v", d)
	}
}

func TestAgentSkillReadsCustomConfigDirs(t *testing.T) {
	p := policy(t)
	f := readsFixture{work: pathutil.Resolved(t.TempDir()), out: pathutil.Resolved(t.TempDir())}
	for _, env := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
		t.Setenv(env, f.out)
		for _, name := range []string{"skills/example/SKILL.md", "plugins/cache/vendor/plugin/1/skills/example/SKILL.md"} {
			file := f.path(name)
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte("instructions\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, a := range []Action{f.read(file), f.exec("cat " + filepath.ToSlash(file))} {
				if d := Evaluate(p, LeadRules{}, mode.Ask, a); d.Kind != Allow {
					t.Errorf("%s %s: %+v", env, name, d)
				}
			}
		}
		t.Setenv(env, "")
	}
}

func TestAgentSkillReadsDoNotFollowEscapingSymlinks(t *testing.T) {
	p := policy(t)
	home, _ := os.UserHomeDir()
	f := readsFixture{work: pathutil.Resolved(t.TempDir()), out: home}
	dir := f.path(".agent/skills/example")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(secret, []byte("private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "SKILL.md")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, a := range []Action{f.read(link), f.read(dir), f.exec("cat " + filepath.ToSlash(link))} {
		if d := Evaluate(p, LeadRules{}, mode.Full, a); d.Kind == Allow {
			t.Errorf("escaping symlink: %+v: %+v", a, d)
		}
	}
}
