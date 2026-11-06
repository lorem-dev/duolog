package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/delegate"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/testbin"
)

func TestDelegateCommandOnlyPrintsAnswer(t *testing.T) {
	isolatedRouteConfig(t)
	claudeGateRuns(t)
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

func TestDelegateRejectsBadFeatures(t *testing.T) {
	isolatedRouteConfig(t)
	t.Chdir(t.TempDir())
	t.Setenv("DUOLOG_DEPTH", "")
	ran := false
	old := runDelegate
	t.Cleanup(func() { runDelegate = old })
	runDelegate = func(context.Context, *config.Config, *store.Store, string, string, string) (delegate.Outcome, error) {
		ran = true
		return delegate.Outcome{}, nil
	}
	out, e := executeBusCommand(t, "delegate", "debug_hard", "--task", "task.md", "--features", "output=poem")
	if e == nil || ran || strings.Count(out, "\n") != 1 || !strings.Contains(out, "diagnosis") {
		t.Fatal(out, e)
	}
}

func TestDelegateUseFlag(t *testing.T) {
	isolatedRouteConfig(t)
	t.Chdir(t.TempDir())
	t.Setenv("DUOLOG_DEPTH", "")
	var got delegate.Options
	old := runDelegate
	t.Cleanup(func() { runDelegate = old })
	runDelegate = func(ctx context.Context, _ *config.Config, _ *store.Store, _, _, _ string) (delegate.Outcome, error) {
		got = delegate.OptionsFrom(ctx)
		return delegate.Outcome{Line: "done=true"}, nil
	}
	out, e := executeBusCommand(t, "delegate", "debug_hard", "--task", "task.md", "--use", "codex/sol/high", "--features", "output=diagnosis", "--class", "debug")
	if e != nil || out != "done=true\n" {
		t.Fatal(out, e)
	}
	if got.Entry == nil || *got.Entry != (config.Target{Agent: "codex", Model: "sol", Effort: "high"}) || !got.Person || got.Features["output"] != "diagnosis" || got.Class != "debug" {
		t.Fatalf("%+v", got)
	}
	got = delegate.Options{}
	out, e = executeBusCommand(t, "delegate", "debug_hard", "--task", "task.md", "--use", "sol/high")
	if e == nil || got.Entry != nil || strings.Count(out, "\n") != 1 || !strings.Contains(out, "agent/model/effort") {
		t.Fatal(out, e)
	}
}

func TestDelegateEscalationFlags(t *testing.T) {
	isolatedRouteConfig(t)
	t.Chdir(t.TempDir())
	t.Setenv("DUOLOG_DEPTH", "")
	var got delegate.Options
	ran := 0
	old := runDelegate
	t.Cleanup(func() { runDelegate = old })
	runDelegate = func(ctx context.Context, _ *config.Config, _ *store.Store, _, _, _ string) (delegate.Outcome, error) {
		got = delegate.OptionsFrom(ctx)
		ran++
		return delegate.Outcome{Line: "done=true"}, nil
	}
	out, e := executeBusCommand(t, "delegate", "debug_hard", "--task", "task.md", "--escalate-from", "task-1", "--reason", "not_green")
	if e != nil || out != "done=true\n" || got.EscalateFrom != "task-1" || got.EscalateReason != "not_green" {
		t.Fatal(out, e, got)
	}
	// One flag without the other is a mistake, not a plain delegation.
	for _, args := range [][]string{{"--escalate-from", "task-1"}, {"--reason", "not_green"}} {
		out, e = executeBusCommand(t, append([]string{"delegate", "debug_hard", "--task", "task.md"}, args...)...)
		if e == nil || ran != 1 || strings.Count(out, "\n") != 1 || !strings.Contains(out, "--escalate-from and --reason go together") {
			t.Fatal(args, out, e)
		}
	}
}
