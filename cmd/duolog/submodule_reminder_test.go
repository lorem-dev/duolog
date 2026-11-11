package main

import (
	"context"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/mcp"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/lead"
	"github.com/lorem-dev/duolog/internal/project"
)

func TestSubmoduleReminderSharedClaim(t *testing.T) {
	d, f, _, _ := submoduleDelegateProject(t)
	out, err := d.mcpDelegate(context.Background(), mcp.DelegateInput{Role: "implement", Task: testTask, Worktree: f.Linked, Wait: &no})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Answer, "Submodules with their own duolog rules: module.") || out.ID == "" || out.AnswerPath == "" {
		t.Fatal(out)
	}
	reg, err := session.Open(project.LeadHome(f.Superproject))
	if err != nil {
		t.Fatal(err)
	}
	if line, err := lead.ClaimSubmoduleReminder(reg, "lead-mcp", f.Submodule); err != nil || line != "" {
		t.Fatal(line, err)
	}
	retry, err := d.mcpDelegate(context.Background(), mcp.DelegateInput{Role: "implement", Task: testTask, Worktree: f.Linked, Wait: &no})
	if err != nil || retry.ID != out.ID || retry.Answer != "" {
		t.Fatal(retry, err)
	}
}
