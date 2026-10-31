package main

import (
	"github.com/lorem-dev/duolog/internal/testbin"
	"os"
	"path/filepath"
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
		"debug models": {Stdout: `{"models":[{"slug":"gpt-6-luna","visibility":"list","supported_reasoning_levels":["low"]}]}`},
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
