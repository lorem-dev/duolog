package runner

import (
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/mode"
)

func TestArgvUsesNativeMode(t *testing.T) {
	s := spec(t, "claude")
	s.Route.Sandbox, s.Mode, s.Native = "workspace-write", mode.Edits, "auto"
	containsArgs(t, Argv(s), "--permission-mode", "auto")
	s.Native = ""
	containsArgs(t, Argv(s), "--permission-mode", "acceptEdits")
}

func TestArgvRefusesNativeAboveMode(t *testing.T) {
	for _, tc := range []struct {
		agent, native, sandbox string
		m                      mode.Mode
		want                   string
	}{
		{"claude", "bypassPermissions", "workspace-write", mode.Edits, "acceptEdits"},
		{"claude", "auto", "read-only", mode.Full, "plan"},
		{"claude", "dontAsk", "workspace-write", mode.Full, "acceptEdits"},
		{"codex", "danger-full-access", "workspace-write", mode.Full, "workspace-write"},
		{"codex", "workspace-write", "workspace-write", mode.Plan, "read-only"},
	} {
		s := spec(t, tc.agent)
		s.Route.Sandbox, s.Mode, s.Native = tc.sandbox, tc.m, tc.native
		line := strings.Join(Argv(s), " ")
		if !strings.Contains(line, " "+tc.want+" ") || strings.Contains(line, "danger-full-access") || strings.Contains(line, "bypassPermissions") && tc.want != "bypassPermissions" {
			t.Errorf("%+v: %s", tc, line)
		}
	}
}

func TestCodexExtrasArgv(t *testing.T) {
	s := spec(t, "codex")
	s.Route.Sandbox, s.Mode, s.Native = "workspace-write", mode.Edits, "workspace-write"
	s.Network, s.WritableRoots = true, []string{"/tmp/a", "/tmp/b"}
	a := Argv(s)
	containsArgs(t, a, "sandbox_workspace_write.network_access=true", `sandbox_workspace_write.writable_roots=["/tmp/a","/tmp/b"]`)
	s.Mode = mode.Plan
	if line := strings.Join(Argv(s), " "); strings.Contains(line, "sandbox_workspace_write") {
		t.Errorf("extras at plan: %s", line)
	}
	s.Mode, s.Native = mode.Edits, "read-only"
	if line := strings.Join(Argv(s), " "); strings.Contains(line, "sandbox_workspace_write") {
		t.Errorf("extras on read-only: %s", line)
	}
	c := spec(t, "claude")
	c.Route.Sandbox, c.Mode, c.Native, c.Network, c.WritableRoots = "workspace-write", mode.Edits, "acceptEdits", true, []string{"/tmp/a"}
	if line := strings.Join(Argv(c), " "); strings.Contains(line, "sandbox_workspace_write") || strings.Contains(line, "/tmp/a") {
		t.Errorf("claude got codex extras: %s", line)
	}
}

// Codex review 4, Claude review I5: every workspace-write launch pins both
// keys, false and empty included, so that Codex's own config files cannot
// widen a worker: at ask, without extras, and on duolog's default flags.
func TestCodexWorkspaceWritePinsExtras(t *testing.T) {
	for _, tc := range []struct {
		name   string
		m      mode.Mode
		native string
		extras bool
	}{
		{"ask with extras", mode.Ask, "workspace-write", true},
		{"edits without extras", mode.Edits, "workspace-write", false},
		{"default flags at edits", mode.Edits, "", true},
		{"default flags at ask", mode.Ask, "", false},
		{"default flags at full", mode.Full, "", false},
	} {
		s := spec(t, "codex")
		s.Route.Sandbox, s.Mode, s.Native = "workspace-write", tc.m, tc.native
		if tc.extras {
			s.Network, s.WritableRoots = true, []string{"/tmp/a"}
		}
		containsArgs(t, Argv(s), "-s", "workspace-write", "-c", "sandbox_workspace_write.network_access=false", "-c", "sandbox_workspace_write.writable_roots=[]")
		if line := strings.Join(Argv(s), " "); strings.Contains(line, "network_access=true") || strings.Contains(line, "/tmp/a") {
			t.Errorf("%s: %s", tc.name, line)
		}
	}
}

// With extras set, every -s pair and every extra still comes before resume.
func TestCodexExtrasBeforeResume(t *testing.T) {
	s := spec(t, "codex")
	s.Route.Sandbox, s.Mode, s.Native = "workspace-write", mode.Edits, "workspace-write"
	s.Network, s.WritableRoots, s.ThreadID = true, []string{"/tmp/a"}, "thread-123"
	a := Argv(s)
	resume := slices.Index(a, "resume")
	if resume < 0 {
		t.Fatal(a)
	}
	for i, arg := range a {
		if (arg == "-s" || strings.HasPrefix(arg, "sandbox_workspace_write.")) && i > resume {
			t.Fatalf("%q after resume: %v", arg, a)
		}
	}
	containsArgs(t, a, "-s", "workspace-write", "sandbox_workspace_write.network_access=true", `sandbox_workspace_write.writable_roots=["/tmp/a"]`)
}

// A looser mode keeps every setting the gate depends on.
func TestLooserArgvKeepsGateSettings(t *testing.T) {
	s := spec(t, "claude")
	s.Route.Sandbox, s.Mode, s.Native = "workspace-write", mode.Full, "bypassPermissions"
	a := Argv(s)
	line := strings.Join(a, " ")
	if strings.Contains(line, "--setting-sources") || strings.Contains(line, "disableAllHooks") || !slices.Contains(a, "--settings") {
		t.Fatal(line)
	}
	containsArgs(t, a, "--permission-mode", "bypassPermissions", "--disallowedTools=Agent,Task")
}
