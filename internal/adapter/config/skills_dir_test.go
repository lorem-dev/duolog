package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func projectConfigPath(root string) string {
	return filepath.Join(root, ".duolog", "config.yaml")
}

func localConfigPath(root string) string {
	return filepath.Join(root, ".duolog", "config.local.yaml")
}

// TestSkillsDirDefaultIsUnset: with no key, nothing names a folder, so the
// adapter keeps its default.
func TestSkillsDirDefaultIsUnset(t *testing.T) {
	root := isolatedRoot(t)
	if _, err := Load(root); err != nil {
		t.Fatal(err)
	}
	dirs, err := ProjectSkillsDirs(root, BuiltinAgents())
	if err != nil || len(dirs) != 0 {
		t.Errorf("ProjectSkillsDirs = %v, %v; want none", dirs, err)
	}
}

// TestSkillsDirIsRead: the key under agents.codex in the project file is read,
// cleaned and kept slash-separated, by Load and by ProjectSkillsDirs.
func TestSkillsDirIsRead(t *testing.T) {
	for in, want := range map[string]string{
		".codex/skills":    ".codex/skills",
		".codex/skills/":   ".codex/skills",
		"./.codex//skills": ".codex/skills",
		"tools/skills":     "tools/skills",
	} {
		root := isolatedRoot(t)
		writeConfig(t, projectConfigPath(root), "agents: {codex: {skills_dir: \""+in+"\"}}\n")
		cfg, err := Load(root)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if got := cfg.Agents["codex"].SkillsDir; got == nil || *got != want {
			t.Errorf("%q: Load read %v, want %q", in, got, want)
		}
		if !cfg.AgentEnabled("codex") {
			t.Errorf("%q: skills_dir switched codex off", in)
		}
		dirs, err := ProjectSkillsDirs(root, BuiltinAgents())
		if err != nil || dirs["codex"] != want {
			t.Errorf("%q: ProjectSkillsDirs = %v, %v; want %q", in, dirs, err, want)
		}
	}
}

// TestSkillsDirLocalWins: config.local.yaml overrides config.yaml.
func TestSkillsDirLocalWins(t *testing.T) {
	root := isolatedRoot(t)
	writeConfig(t, projectConfigPath(root), "agents: {codex: {skills_dir: a/skills}}\n")
	writeConfig(t, localConfigPath(root), "agents: {codex: {skills_dir: b/skills}}\n")
	dirs, err := ProjectSkillsDirs(root, BuiltinAgents())
	if err != nil || dirs["codex"] != "b/skills" {
		t.Errorf("ProjectSkillsDirs = %v, %v; want b/skills", dirs, err)
	}
}

// TestSkillsDirOnlyInProjectFiles: the user config refuses the key, because a
// folder is a property of the repository, and release 1.5.0 refuses it anyway.
func TestSkillsDirOnlyInProjectFiles(t *testing.T) {
	root := isolatedRoot(t)
	writeConfig(t, userConfigPath(t), "agents: {codex: {skills_dir: .codex/skills}}\n")
	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "agents.codex.skills_dir") || !strings.Contains(err.Error(), "project") {
		t.Errorf("got %v, want an error that names agents.codex.skills_dir and the project files", err)
	}
	root = isolatedRoot(t)
	writeConfig(t, localConfigPath(root), "agents: {codex: {skills_dir: .codex/skills}}\n")
	if _, err := Load(root); err != nil {
		t.Errorf("config.local.yaml: %v", err)
	}
}

// TestSkillsDirRefused: an empty value, an absolute path, a path that leaves
// the project and a folder that holds or sits in another agent folder fail
// with an error that names the key.
func TestSkillsDirRefused(t *testing.T) {
	for _, in := range []string{
		"", "  ", "/etc/skills", `C:\skills`, "C:/skills", `.codex\skills`, "..", "../skills", "a/../../skills", ".",
		".git/skills", ".git", ".GIT/skills", ".Git",
		".claude", ".claude/skills", ".Claude/Skills", ".claude/skills/x", ".claude/agents", ".CLAUDE/agents/x",
		".codex", ".CODEX", ".codex/agents", ".Codex/Agents/x",
		".duolog", ".duolog/skills", ".DUOLOG",
		// Windows drops a trailing dot or space, so these name folders above.
		".claude./skills", ".claude /skills", ".codex./agents", ".duolog./x", "tools./skills", "tools/skills.", "tools/skills ",
	} {
		root := isolatedRoot(t)
		writeConfig(t, projectConfigPath(root), "agents: {codex: {skills_dir: '"+in+"'}}\n")
		_, err := Load(root)
		if err == nil || !strings.Contains(err.Error(), "agents.codex.skills_dir") {
			t.Errorf("%q: Load got %v, want an error that names agents.codex.skills_dir", in, err)
		}
		if _, err := ProjectSkillsDirs(root, BuiltinAgents()); err == nil || !strings.Contains(err.Error(), "agents.codex.skills_dir") {
			t.Errorf("%q: ProjectSkillsDirs got %v, want an error that names agents.codex.skills_dir", in, err)
		}
	}
	// A folder next to these is fine.
	for _, in := range []string{".codex/skills", ".agents/skills", ".Agents/skills", ".claude-skills", "docs/.codex"} {
		root := isolatedRoot(t)
		writeConfig(t, projectConfigPath(root), "agents: {codex: {skills_dir: '"+in+"'}}\n")
		if _, err := Load(root); err != nil {
			t.Errorf("%q: %v", in, err)
		}
	}
}

// TestSkillsDirOnlyForCodex: another agent has no such key.
func TestSkillsDirOnlyForCodex(t *testing.T) {
	root := isolatedRoot(t)
	writeConfig(t, projectConfigPath(root), "agents: {claude: {skills_dir: .claude/skills}}\n")
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "config.agents.claude.skills_dir: unknown key") {
		t.Errorf("got %v, want an unknown key error", err)
	}
}
