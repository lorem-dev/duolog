package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/testbin"
)

func TestSkillTestCodexAndOverwrite(t *testing.T) {
	root := skillProject(t)
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[mcp_servers.private]\ncommand = 'never-start-this'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fake := testbin.Install(t, "codex", withTestModelCatalog(t, testbin.Scenario{Sequence: []testbin.Scenario{
		{Answer: `{"s1":"yes"}`, Stdout: "{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":100,\"cached_input_tokens\":20,\"output_tokens\":10}}\n"},
		{Answer: "not JSON", Exit: 1},
	}}, "luna"))
	args := []string{"skill", "test", "skills/demo/SKILL.md", "--models", "codex/luna/high", "--yes"}
	out, err := executeBusCommand(t, args...)
	if err != nil || !strings.Contains(out, "1/1") {
		t.Fatalf("%s %v", out, err)
	}
	data, err := os.ReadFile(fake + ".args.json")
	if err != nil {
		t.Fatal(err)
	}
	var argv []string
	if err := json.Unmarshal(data, &argv); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, "|")
	for _, want := range []string{`mcp_servers."private".enabled=false`, "features.shell_tool=false", "features.apply_patch_freeform=false", "--skip-git-repo-check"} {
		if !strings.Contains(joined, want) {
			t.Errorf("%s lacks %s", joined, want)
		}
	}
	out, err = executeBusCommand(t, args...)
	if err == nil || !strings.Contains(out, "0/1") || !strings.Contains(out, "--") {
		t.Fatalf("%s %v", out, err)
	}
	data, err = os.ReadFile(filepath.Join(root, ".duolog/skill-tests/demo/codex-luna-high.json"))
	if err != nil || bytes.Contains(data, []byte(`\"yes\"`)) || !bytes.Contains(data, []byte("not JSON")) {
		t.Fatalf("%s %v", data, err)
	}
	data, err = os.ReadFile(fake + ".cwd")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(string(data)); !os.IsNotExist(err) {
		t.Fatal("temporary run directory remains")
	}
}

func TestSkillTestSkipsOtherAgents(t *testing.T) {
	root := skillProject(t)
	plan := `situations:
  - {id: shared, text: Shared question, check: {starts: yes}}
  - {id: only-claude, agents: [claude], text: Claude question, check: {starts: yes}}
  - {id: only-codex, agents: [codex], text: Codex question, check: {starts: yes}}
`
	if err := os.WriteFile(filepath.Join(root, ".duolog/skill-tests/demo.plan.yaml"), []byte(plan), 0600); err != nil {
		t.Fatal(err)
	}
	claude := testbin.Install(t, "claude", testbin.Scenario{Stdout: `{"result":"{\"shared\":\"yes\",\"only-claude\":\"yes\"}"}`})
	codex := testbin.Install(t, "codex", withTestModelCatalog(t, testbin.Scenario{Answer: `{"shared":"yes","only-codex":"no"}`}, "luna"))
	out, err := executeBusCommand(t, "skill", "test", "skills/demo/SKILL.md", "--models", "claude/haiku/low,codex/luna/low", "--yes")
	if err != nil {
		t.Fatalf("%s %v", out, err)
	}
	for _, row := range []string{"shared ok ok", "only-claude ok ..", "only-codex .. --", "score 2/2 1/2"} {
		if !strings.Contains(strings.Join(strings.Fields(out), " "), row) {
			t.Errorf("missing %q in %s", row, out)
		}
	}
	for _, agent := range []struct{ bin, excluded string }{{claude, "Codex question"}, {codex, "Claude question"}} {
		data, err := os.ReadFile(agent.bin + ".stdin")
		if err != nil || bytes.Contains(data, []byte(agent.excluded)) {
			t.Fatalf("prompt %s %v", data, err)
		}
	}
}

func TestSkillTestAllSituationsSkipped(t *testing.T) {
	root := skillProject(t)
	if err := os.WriteFile(filepath.Join(root, ".duolog/skill-tests/demo.plan.yaml"), []byte("situations: [{id: codex, agents: [codex], text: Codex question, check: {starts: yes}}]"), 0600); err != nil {
		t.Fatal(err)
	}
	fake := testbin.Install(t, "claude", testbin.Scenario{})
	out, err := executeBusCommand(t, "skill", "test", "skills/demo/SKILL.md", "--models", "claude/haiku/low", "--yes")
	if err != nil || !strings.Contains(strings.Join(strings.Fields(out), " "), "codex .. score 0/0") {
		t.Fatalf("%s %v", out, err)
	}
	if _, err := os.Stat(fake + ".args.json"); !os.IsNotExist(err) {
		t.Fatal("ran a model with no matching situations")
	}
}

func TestSkillTestExplicitPlanAndConsent(t *testing.T) {
	root := skillProject(t)
	if err := os.Rename(filepath.Join(root, ".duolog/skill-tests/demo.plan.yaml"), filepath.Join(root, "other.yaml")); err != nil {
		t.Fatal(err)
	}
	testbin.Install(t, "claude", testbin.Scenario{Stdout: `{"result":"{\"s1\":\"yes\"}"}`})
	cmd := newRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader("yes\n"))
	cmd.SetArgs([]string{"skill", "test", "skills/demo/SKILL.md", "--plan", "other.yaml", "--models", "claude/haiku/low"})
	if err := cmd.Execute(); err != nil || !strings.Contains(out.String(), "1/1") {
		t.Fatalf("%s %v", out.String(), err)
	}
}

func skillProject(t *testing.T) string {
	t.Helper()
	isolatedRouteConfig(t)
	t.Chdir(t.TempDir())
	root, _ := os.Getwd()
	for name, data := range map[string]string{
		"skills/demo/SKILL.md":               "---\nname: demo\n---\nReply yes.\n",
		".duolog/skill-tests/demo.plan.yaml": "situations: [{id: s1, text: 'What now?', check: {starts: yes}}]\n",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestSkillTestCommand(t *testing.T) {
	root := skillProject(t)
	fake := testbin.Install(t, "claude", testbin.Scenario{Stdout: `{"result":"{\"s1\":\"YES\"}","usage":{"input_tokens":100,"output_tokens":10}}`})
	out, err := executeBusCommand(t, "skill", "test", "skills/demo/SKILL.md", "--models", "claude/haiku/low", "--yes")
	if err != nil || !strings.Contains(out, "1 run") || !strings.Contains(out, "1/1") || !strings.Contains(out, "ok") || !strings.Contains(out, "Total cost") {
		t.Fatalf("%s %v", out, err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".duolog/skill-tests/demo/claude-haiku-low.json"))
	if err != nil || !bytes.Contains(data, []byte("YES")) {
		t.Fatalf("%s %v", data, err)
	}
	data, err = os.ReadFile(filepath.Join(root, ".duolog/usage.ndjson"))
	if err != nil || !bytes.Contains(data, []byte(`"role":"skill_test"`)) {
		t.Fatalf("%s %v", data, err)
	}
	data, err = os.ReadFile(fake + ".stdin")
	if err != nil || !bytes.Contains(data, []byte("What now?")) {
		t.Fatalf("%s %v", data, err)
	}
}

func TestSkillTestConsentAndRemember(t *testing.T) {
	root := skillProject(t)
	fake := testbin.Install(t, "claude", testbin.Scenario{Stdout: `{"result":"{\"s1\":\"YES\"}"}`})
	out, err := executeBusCommand(t, "skill", "test", "skills/demo/SKILL.md", "--models", "claude/haiku/low")
	if err != nil || !strings.Contains(out, "Run these tests?") {
		t.Fatalf("%s %v", out, err)
	}
	if _, err := os.Stat(fake + ".args.json"); !os.IsNotExist(err) {
		t.Fatal("ran without consent")
	}
	path := filepath.Join(root, ".duolog/config.local.yaml")
	if err := os.WriteFile(path, []byte("# keep this\nversion: 1\nsync:\n  auto: false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := executeBusCommand(t, "skill", "test", "--remember", "always"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !bytes.Contains(data, []byte("keep this")) || !bytes.Contains(data, []byte("auto: false")) || !bytes.Contains(data, []byte("skill_tests: always")) {
		t.Fatalf("%s", data)
	}
	out, err = executeBusCommand(t, "skill", "test", "skills/demo/SKILL.md", "--models", "claude/haiku/low")
	if err != nil || !strings.Contains(out, "1/1") {
		t.Fatalf("%s %v", out, err)
	}
	if _, err := executeBusCommand(t, "skill", "test", "--remember", "never"); err != nil {
		t.Fatal(err)
	}
	out, err = executeBusCommand(t, "skill", "test", "skills/demo/SKILL.md", "--models", "claude/haiku/low")
	if err != nil || !strings.Contains(out, "never") {
		t.Fatalf("%s %v", out, err)
	}
}

func TestSkillTestWorkersAndMissingPlan(t *testing.T) {
	root := skillProject(t)
	if err := os.Remove(filepath.Join(root, ".duolog/skill-tests/demo.plan.yaml")); err != nil {
		t.Fatal(err)
	}
	out, err := executeBusCommand(t, "skill", "test", "skills/demo/SKILL.md")
	if err != nil || !strings.Contains(out, "demo.plan.yaml") {
		t.Fatalf("%s %v", out, err)
	}
	t.Setenv("DUOLOG_DEPTH", "1")
	for _, args := range [][]string{{"skill", "test", "skills/demo/SKILL.md", "--yes"}, {"skill", "test", "--remember", "always"}} {
		if _, err := executeBusCommand(t, args...); err == nil {
			t.Fatal("worker accepted")
		}
	}
}
