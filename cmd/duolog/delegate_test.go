package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/testbin"
)

func TestDelegateCommandOnlyPrintsAnswer(t *testing.T) {
	isolatedRouteConfig(t)
	t.Setenv("DUOLOG_DEPTH", "")
	t.Setenv("DUOLOG_LEAD", "")
	root := t.TempDir()
	t.Chdir(root)
	taskPath := filepath.Join(root, "task.md")
	for p, s := range map[string]string{taskPath: "# Task: command\n\n## Goal\nDo work.\n\n## Read\n- a.go\n\n## Write\nNone.\n\n## Limits\nNo changes.\n\n## Check\nReview.\n", filepath.Join(root, "a.go"): "PRIVATE_COMMAND_CONTENT\n"} {
		if e := os.WriteFile(p, []byte(s), 0600); e != nil {
			t.Fatal(e)
		}
	}
	fake := testbin.Install(t, "claude", testbin.Scenario{Stdout: `{"structured_output":{"done":true,"summary":"Reviewed.","files":["a.go"],"needs":[]}}`})
	s, e := store.Open(root)
	if e != nil {
		t.Fatal(e)
	}
	reg, e := session.Open(s.Dir)
	if e != nil {
		t.Fatal(e)
	}
	if e := reg.RegisterLead(session.Lead{ID: "registered-lead", Target: config.Target{Agent: "codex", Model: "sol", Effort: "medium"}, Mode: mode.Plan}); e != nil {
		t.Fatal(e)
	}
	out, e := executeBusCommand(t, "delegate", "review_final", "--task", taskPath, "--lead", "registered-lead", "--thread", "topic")
	if e != nil || strings.Count(out, "\n") != 1 || !strings.Contains(out, "done=true") || !strings.Contains(out, "Reviewed.") || !strings.Contains(out, "a.go") || strings.Contains(out, "PRIVATE_COMMAND_CONTENT") {
		t.Fatal(out, e)
	}
	data, e := os.ReadFile(fake + ".env.json")
	if e != nil || !strings.Contains(string(data), "DUOLOG_LEAD=registered-lead") {
		t.Fatal(string(data), e)
	}
	data, e = os.ReadFile(filepath.Join(s.Dir, "bus.ndjson"))
	if e != nil || strings.Contains(string(data), "PRIVATE_COMMAND_CONTENT") {
		t.Fatal(string(data), e)
	}
	var line map[string]any
	if e := json.Unmarshal([]byte(strings.Split(string(data), "\n")[0]), &line); e != nil || line["from"] != "codex/lead.regist" {
		t.Fatal(line, e)
	}
}
func TestDelegateCommandRefusalIsOneLine(t *testing.T) {
	isolatedRouteConfig(t)
	t.Chdir(t.TempDir())
	t.Setenv("DUOLOG_DEPTH", "1")
	out, e := executeBusCommand(t, "delegate", "implement", "--task", "task.md")
	if e == nil || strings.Count(out, "\n") != 1 || !strings.Contains(out, "nested") {
		t.Fatal(out, e)
	}
}
