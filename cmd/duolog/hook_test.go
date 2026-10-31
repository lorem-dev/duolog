package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
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

type hookInputReader struct {
	io.Reader
	beforeRead func()
}

func (r *hookInputReader) Read(p []byte) (int, error) {
	if r.beforeRead != nil {
		before := r.beforeRead
		r.beforeRead = nil
		before()
	}
	return r.Reader.Read(p)
}

func TestLeadCommandsCaptureTimeBeforeStdin(t *testing.T) {
	for _, args := range [][]string{
		{"hook", "SessionStart", "--agent", "claude"},
		{"gate", "--agent", "claude", "--event", "PreToolUse"},
	} {
		t.Run(args[0], func(t *testing.T) {
			isolatedRouteConfig(t)
			t.Setenv("DUOLOG_DEPTH", "")
			t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
			root := t.TempDir()
			t.Chdir(root)
			reg, err := session.Open(filepath.Join(root, ".duolog"))
			if err != nil {
				t.Fatal(err)
			}
			cmd := newRootCommand()
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(args)
			cmd.SetIn(&hookInputReader{
				Reader: strings.NewReader(`{"session_id":"lead","permission_mode":"full"}`),
				beforeRead: func() {
					// A newer restrictive hook finishes while the older command
					// is reading stdin, before it has read the registry.
					plan := mode.Plan
					if _, err := reg.ObserveLead(session.LeadObservation{ID: "lead", Agent: "claude", Mode: &plan}, time.Now()); err != nil {
						t.Fatal(err)
					}
				},
			})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			lead, ok, err := reg.Lead("lead")
			if err != nil || !ok || lead.Mode != mode.Plan {
				t.Fatal("lost observation during stdin read", lead, ok, err)
			}
		})
	}
}
