package install

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/plugin"
)

func TestUpdateSkillPermissions(t *testing.T) {
	// Kills pre-allowing writes, losing the update trigger, or shipping an unversioned skill.
	for _, agent := range []string{"claude", "codex"} {
		front, _ := splitSkill(t, agent+"/skills/duolog-update/SKILL.md")
		for _, want := range []string{"name: duolog-update", "Use when the person asks to update duolog", "duolog-version: \"" + Version() + "\""} {
			if !strings.Contains(front, want) {
				t.Errorf("%s frontmatter lacks %q", agent, want)
			}
		}
		allowed := ""
		for _, line := range strings.Split(front, "\n") {
			if strings.HasPrefix(line, "allowed-tools:") {
				allowed = line
			}
		}
		want := "allowed-tools: Bash(duolog version), Bash(duolog doctor)"
		if agent == "codex" {
			want = ""
		}
		if allowed != want {
			t.Errorf("%s: %q, want %q", agent, allowed, want)
		}
	}
}

// TestUpdateSkillWorkflow checks the update order and both plugin paths:
// adding a missing marketplace, or updating one already registered.
func TestUpdateSkillWorkflow(t *testing.T) {
	// Kills missing or reordered update stages and updating the wrong agent's plugin.
	for agent, commands := range map[string][]string{
		"claude": {"claude plugin marketplace update duolog", "claude plugin update duolog@duolog"},
		"codex":  {"codex plugin marketplace upgrade duolog", "codex plugin add duolog@duolog"},
	} {
		_, body := splitSkill(t, agent+"/skills/duolog-update/SKILL.md")
		stages := []string{"duolog version", "curl -fsSL https://github.com/lorem-dev/duolog/releases/latest/download/install.sh | DUOLOG_VERSION=latest sh", "$env:DUOLOG_VERSION = 'latest'; irm https://github.com/lorem-dev/duolog/releases/latest/download/install.ps1 | iex", "duolog version"}
		verb := "install"
		if agent == "codex" {
			verb = "add"
		}
		stages = append(stages, agent+" plugin marketplace list", agent+" plugin marketplace add lorem-dev/duolog", agent+" plugin "+verb+" duolog@duolog")
		stages = append(stages, commands...)
		stages = append(stages, "duolog install --scope project", "duolog install --scope user", "/hooks", "duolog doctor", "new Claude Code and Codex sessions")
		rest := body
		for _, stage := range stages {
			_, after, ok := strings.Cut(rest, stage)
			if !ok {
				t.Fatalf("%s: missing or misplaced %q", agent, stage)
			}
			rest = after
		}
	}
}

func TestUpdateSkillLimits(t *testing.T) {
	// Kills omitting failure, scope, or human approval instructions, and oversized skills.
	for _, agent := range []string{"claude", "codex"} {
		text := bundled(t, agent, "duolog-update", "SKILL.md")
		if len(strings.Split(strings.TrimSpace(text), "\n")) >= 60 {
			t.Errorf("%s: skill is not under 60 lines", agent)
		}
		text = strings.Join(strings.Fields(text), " ")
		for _, want := range []string{"Stop at the first command that fails", "show its error", "permission prompt to the person", "already the newest release", ".duolog/config.yaml", "Do not install into a project", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "settings.json", "hooks.json", "user skills alone", "skips a changed hook", "old hooks and skills"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s lacks %q", agent, want)
			}
		}
	}
}

func TestUpdateSkillInstallAndRemove(t *testing.T) {
	// Kills leaving the skill out of install, missing-skill checks, or uninstall.
	if !slices.Contains(plugin.Skills, "duolog-update") {
		t.Fatal("update skill is not registered")
	}
	for _, scope := range []string{"project", "user"} {
		home, root := setup(t)
		o := Options{Scope: scope, Agent: "both", Root: root, Home: home}
		run(t, o)
		for _, agent := range []string{"claude", "codex"} {
			path := userSkill(home, agent, "duolog-update")
			if read(t, path) != bundled(t, agent, "duolog-update", "SKILL.md") {
				t.Fatal("installed skill differs")
			}
			if err := os.RemoveAll(filepath.Dir(path)); err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(check(t, o)[agent].MissingSkills, filepath.Dir(path)) {
				t.Fatalf("%s: missing skill not detected", agent)
			}
		}
		run(t, o)
		if err := Remove(o); err != nil {
			t.Fatal(err)
		}
		for _, agent := range []string{"claude", "codex"} {
			if _, err := os.Stat(userSkill(home, agent, "duolog-update")); !os.IsNotExist(err) {
				t.Fatalf("uninstalled skill remains: %v", err)
			}
		}
	}
}
