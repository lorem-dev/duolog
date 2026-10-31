package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/session"
)

func TestHookCLIRegistersAndEndsSilently(t *testing.T) {
	isolatedRouteConfig(t)
	t.Setenv("DUOLOG_DEPTH", "")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	root := t.TempDir()
	t.Chdir(root)
	r, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	input := `{"session_id":"cli-lead","permission_mode":"default"}`
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "SessionEnd"} {
		var out, stderr bytes.Buffer
		cmd := newRootCommand()
		cmd.SetOut(&out)
		cmd.SetErr(&stderr)
		cmd.SetIn(strings.NewReader(input))
		cmd.SetArgs([]string{"hook", event, "--agent", "claude"})
		if err = cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if event == "SessionStart" {
			if !json.Valid(out.Bytes()) {
				t.Fatal(out.String())
			}
		} else if out.Len() != 0 {
			t.Fatal(out.String())
		}
		if stderr.Len() != 0 {
			t.Fatal(stderr.String())
		}
	}
	if _, ok, err := r.Lead("cli-lead"); err != nil || ok {
		t.Fatal(ok, err)
	}
}
func TestHookCLISilentOnBadInputOrUninitialized(t *testing.T) {
	isolatedRouteConfig(t)
	t.Setenv("DUOLOG_DEPTH", "")
	t.Chdir(t.TempDir())
	for _, input := range []string{"bad", `{"session_id":"abc"}`} {
		var out bytes.Buffer
		cmd := newRootCommand()
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetIn(strings.NewReader(input))
		cmd.SetArgs([]string{"hook", "SessionStart", "--agent", "codex"})
		if err := cmd.Execute(); err != nil || out.Len() != 0 {
			t.Fatal(out.String(), err)
		}
	}
	if _, err := os.Stat(".duolog"); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestHookCLIWakeExitTwoAndStderr(t *testing.T) {
	isolatedRouteConfig(t)
	t.Setenv("DUOLOG_DEPTH", "")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	root := t.TempDir()
	t.Chdir(root)
	r, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if err = r.RegisterLead(session.Lead{ID: "wake-lead", Target: config.Target{Agent: "claude"}}); err != nil {
		t.Fatal(err)
	}
	raw := `{"from":"other","to":"claude/lead.wake-l","s":"wake now"}` + "\n"
	if err = os.WriteFile(filepath.Join(r.Dir, "bus.ndjson"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	cmd := newRootCommand()
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetIn(strings.NewReader(`{"session_id":"wake-lead"}`))
	cmd.SetArgs([]string{"hook", "wake", "--agent", "claude"})
	err = cmd.Execute()
	if err == nil || exitCode(err) != 2 || out.Len() != 0 || stderr.String() != raw {
		t.Fatal(out.String(), stderr.String(), err)
	}
}
