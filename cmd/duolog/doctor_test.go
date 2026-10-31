package main

import (
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/testbin"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDoctorExplainsHookTrustLimit(t *testing.T) {
	isolatedRouteConfig(t)
	t.Chdir(t.TempDir())
	codex := testbin.Install(t, "codex", testbin.Scenario{Stdout: "{}"})
	claude := testbin.Install(t, "claude", testbin.Scenario{})
	t.Setenv("PATH", filepath.Dir(codex)+string(os.PathListSeparator)+filepath.Dir(claude))
	out, err := executeBusCommand(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hooks approved=false", "changed after review cannot be detected", "Codex itself will skip", "/hooks"} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor output %q missing %q", out, want)
		}
	}
}

func TestDoctorProbeFlag(t *testing.T) {
	isolatedRouteConfig(t)
	t.Chdir(t.TempDir())
	codex := testbin.Install(t, "codex", testbin.Scenario{Responses: map[string]testbin.Scenario{
		"--version":    {Stdout: "codex-cli 0.165.0"},
		"debug models": {Stdout: `{"models":[{"slug":"gpt-6-luna","visibility":"list","supported_reasoning_levels":[{"effort":"low","description":"d"}]}]}`},
		"exec":         {Writes: map[string]string{"answer.json": "ok"}},
	}})
	claude := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
		"--version": {Stdout: "2.1.312 (Claude Code)"},
		"-p":        {Stdout: `{"is_error":false,"result":"ok"}`},
	}})
	t.Setenv("PATH", filepath.Dir(codex)+string(os.PathListSeparator)+filepath.Dir(claude))
	out, err := executeBusCommand(t, "doctor", "--probe")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"codex probe succeeded", "claude probe succeeded"} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q missing %q", out, want)
		}
	}
}

func TestDoctorUsesRexInventoryAndLeadState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Rex has no Windows build")
	}
	isolatedRouteConfig(t)
	root := t.TempDir()
	t.Chdir(root)
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	for _, lead := range []string{"ended", "live"} {
		if err := reg.RegisterLead(session.Lead{ID: lead}); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []session.Entry{
		{ID: "peer-a", Block: "block:orphan-a", Lead: "ended"},
		{ID: "peer-b", Block: "block:orphan-b", Lead: "ended"},
		{ID: "peer-gone", Block: "block:gone", Lead: "ended"},
		{ID: "peer-live", Block: "block:live", Lead: "live"},
		// An ID that looks like a block must not be taken for one: only Block counts.
		{ID: "block:trap", Lead: "ended"},
	} {
		if err := reg.Record(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.Close("ended"); err != nil {
		t.Fatal(err)
	}
	rex := testbin.Install(t, "rex", testbin.Scenario{Exit: 2, Responses: map[string]testbin.Scenario{
		"block ls -s session:s1 --json": {Stdout: `{"blocks":[{"block_id":"block:orphan-a","label":"shell"},{"block_id":"block:orphan-b"},{"block_id":"block:live"},{"block_id":"block:unrelated"},{"block_id":"block:trap"}]}`},
	}})
	// Rex is not in PATH, as in a real Rex session; REX_BIN_DIR finds it.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("REX_BIN_DIR", filepath.Dir(rex))
	t.Setenv("REX_SESSION", "session:s1")
	out, err := executeBusCommand(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"block:orphan-a", "block:orphan-b"} {
		if !strings.Contains(out, "Pane "+id+" belongs to ended lead ended; duolog peer close "+id) {
			t.Fatal(out)
		}
	}
	for _, id := range []string{"block:live", "block:gone", "block:unrelated", "block:trap"} {
		if strings.Contains(out, "duolog peer close "+id) {
			t.Fatal(out)
		}
	}
	calls, err := os.ReadFile(rex + ".calls.ndjson")
	if err != nil || string(calls) != "[\"block\",\"ls\",\"-s\",\"session:s1\",\"--json\"]\n" {
		t.Fatalf("calls=%s err=%v", calls, err)
	}
}

func TestDoctorRexInventoryUnavailable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Rex has no Windows build")
	}
	for _, scenario := range []testbin.Scenario{
		{Exit: 4, Stderr: "socket unavailable"},
		{Stdout: "invalid JSON"},
		{Stdout: `{}`},
	} {
		t.Run(scenario.Stdout+scenario.Stderr, func(t *testing.T) {
			isolatedRouteConfig(t)
			t.Chdir(t.TempDir())
			rex := testbin.Install(t, "rex", scenario)
			t.Setenv("PATH", filepath.Dir(rex))
			t.Setenv("REX_BIN_DIR", "")
			t.Setenv("REX_SESSION", "")
			out, err := executeBusCommand(t, "doctor")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "Rex pane inventory unavailable") {
				t.Fatal(out)
			}
		})
	}
}
