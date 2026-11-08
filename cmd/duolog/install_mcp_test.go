package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/testbin"
)

// deferredLine is doctor's line for a lead whose Claude loads every tool
// schema in full.
const deferredLine = "duolog MCP: deferred tool loading is off (ENABLE_TOOL_SEARCH=false), so its four tools load in full every turn; turn it on or run duolog uninstall to drop the server"

func TestInstallCheckExitsStaleWithoutServer(t *testing.T) {
	_, home := installedProject(t)
	state := filepath.Join(home, ".claude.json")
	data, err := os.ReadFile(state)
	if err != nil || !strings.Contains(string(data), `"duolog"`) {
		t.Fatalf("install wrote no server entry: %s %v", data, err)
	}
	if out, err := executeBusCommand(t, "install", "--check"); err != nil {
		t.Fatal(out, err)
	}
	if err := os.WriteFile(state, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := executeBusCommand(t, "install", "--check")
	if err == nil || exitCode(err) != 1 {
		t.Fatalf("err %v, output %q", err, out)
	}
	if !strings.Contains(out, "claude project install is out of date: "+state) || !strings.Contains(out, "codex project install is current") {
		t.Fatalf("output %q", out)
	}
	if got, _ := os.ReadFile(state); string(got) != "{}\n" {
		t.Fatalf("install --check wrote %s", got)
	}
}

// doctorWithInstall runs doctor in an installed project with fake agents.
func doctorWithInstall(t *testing.T) (root string) {
	t.Helper()
	root, _ = installedProject(t)
	codex := testbin.Install(t, "codex", testbin.Scenario{Stdout: "{}"})
	claude := testbin.Install(t, "claude", testbin.Scenario{})
	t.Setenv("PATH", filepath.Dir(codex)+string(os.PathListSeparator)+filepath.Dir(claude))
	fakeRexFirst(t)
	return root
}

func TestDoctorSaysDeferredLoadingOff(t *testing.T) {
	root := doctorWithInstall(t)
	t.Setenv("ENABLE_TOOL_SEARCH", "false")
	out, err := executeBusCommand(t, "doctor")
	if err != nil {
		t.Fatal(out, err)
	}
	if !strings.Contains(out, "warning: "+deferredLine+"\n") {
		t.Fatalf("doctor output %q missing %q", out, deferredLine)
	}
	// The setting in the project's Claude settings counts as well.
	t.Setenv("ENABLE_TOOL_SEARCH", "")
	settings := filepath.Join(root, ".claude", "settings.local.json")
	if err := os.WriteFile(settings, []byte(`{"env": {"ENABLE_TOOL_SEARCH": "false"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := executeBusCommand(t, "doctor"); err != nil || !strings.Contains(out, deferredLine) {
		t.Fatalf("err %v, doctor output %q", err, out)
	}
}

func TestDoctorQuietWhenDeferredLoadingOn(t *testing.T) {
	doctorWithInstall(t)
	for _, value := range []string{"", "true", "auto"} {
		t.Setenv("ENABLE_TOOL_SEARCH", value)
		out, err := executeBusCommand(t, "doctor")
		if err != nil {
			t.Fatal(out, err)
		}
		if strings.Contains(out, "deferred tool loading") || strings.Contains(out, "claude project install is out of date") {
			t.Fatalf("ENABLE_TOOL_SEARCH=%q: doctor output %q", value, out)
		}
	}
}
