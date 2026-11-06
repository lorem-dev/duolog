package wiring

import (
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/mode"
)

// launched is the argv of s with its process built for s's launch words, as
// a caller builds the process it has admitted.
func launched(t *testing.T, s Spec) []string {
	t.Helper()
	return Argv(rebuilt(t, s))
}

func TestArgvUsesNativeMode(t *testing.T) {
	s := spec(t, "claude")
	s.Route.Sandbox, s.Mode, s.Native = "workspace-write", mode.Edits, "auto"
	containsArgs(t, launched(t, s), "--permission-mode", "auto")
	s.Native = ""
	containsArgs(t, launched(t, s), "--permission-mode", "acceptEdits")
}

func TestArgvRefusesNativeAboveMode(t *testing.T) {
	for _, tc := range []struct {
		agent, native, sandbox string
		m                      mode.Mode
		want                   string
	}{
		{"claude", "bypassPermissions", "workspace-write", mode.Edits, "acceptEdits"},
		{"claude", "auto", "read-only", mode.Full, "default"},
		{"claude", "dontAsk", "workspace-write", mode.Full, "acceptEdits"},
		{"codex", "danger-full-access", "workspace-write", mode.Full, "workspace-write"},
		{"codex", "workspace-write", "workspace-write", mode.Plan, "read-only"},
	} {
		s := spec(t, tc.agent)
		s.Route.Sandbox, s.Mode, s.Native = tc.sandbox, tc.m, tc.native
		line := strings.Join(launched(t, s), " ")
		if !strings.Contains(line, " "+tc.want+" ") || strings.Contains(line, "danger-full-access") || strings.Contains(line, "bypassPermissions") && tc.want != "bypassPermissions" {
			t.Errorf("%+v: %s", tc, line)
		}
	}
}

// A read-only role's Claude worker starts in Claude's default mode, never in
// plan, whatever native name or level it is given; the gate keeps it
// read-only. A Codex worker keeps its read-only sandbox.
func TestReadOnlyClaudeWorkerNeverLaunchesInPlan(t *testing.T) {
	for _, native := range []string{"", "plan", "default", "acceptEdits"} {
		for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Full} {
			s := spec(t, "claude")
			s.Route.Sandbox, s.Mode, s.Native = "read-only", m, native
			args := launched(t, s)
			containsArgs(t, args, "--permission-mode", "default")
			if slices.Contains(args, "plan") || slices.Contains(Launch(s), "plan") {
				t.Errorf("%q at %s: %v", native, m, args)
			}
		}
	}
	c := spec(t, "codex")
	c.Route.Sandbox, c.Mode, c.Native = "read-only", mode.Plan, "read-only"
	containsArgs(t, launched(t, c), "-s", "read-only")
}

func TestCodexExtrasArgv(t *testing.T) {
	s := spec(t, "codex")
	s.Route.Sandbox, s.Mode, s.Native = "workspace-write", mode.Edits, "workspace-write"
	s.Network, s.WritableRoots = true, []string{"/tmp/a", "/tmp/b"}
	a := launched(t, s)
	containsArgs(t, a, "sandbox_workspace_write.network_access=true", `sandbox_workspace_write.writable_roots=["/tmp/a","/tmp/b"]`)
	s.Mode = mode.Plan
	if line := strings.Join(launched(t, s), " "); strings.Contains(line, "sandbox_workspace_write") {
		t.Errorf("extras at plan: %s", line)
	}
	s.Mode, s.Native = mode.Edits, "read-only"
	if line := strings.Join(launched(t, s), " "); strings.Contains(line, "sandbox_workspace_write") {
		t.Errorf("extras on read-only: %s", line)
	}
	c := spec(t, "claude")
	c.Route.Sandbox, c.Mode, c.Native, c.Network, c.WritableRoots = "workspace-write", mode.Edits, "acceptEdits", true, []string{"/tmp/a"}
	if line := strings.Join(launched(t, c), " "); strings.Contains(line, "sandbox_workspace_write") || strings.Contains(line, "/tmp/a") {
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
		containsArgs(t, launched(t, s), "-s", "workspace-write", "-c", "sandbox_workspace_write.network_access=false", "-c", "sandbox_workspace_write.writable_roots=[]")
		if line := strings.Join(launched(t, s), " "); strings.Contains(line, "network_access=true") || strings.Contains(line, "/tmp/a") {
			t.Errorf("%s: %s", tc.name, line)
		}
	}
}

// With extras set, every -s pair and every extra still comes before resume.
func TestCodexExtrasBeforeResume(t *testing.T) {
	s := spec(t, "codex")
	s.Route.Sandbox, s.Mode, s.Native = "workspace-write", mode.Edits, "workspace-write"
	s.Network, s.WritableRoots, s.ThreadID = true, []string{"/tmp/a"}, "thread-123"
	a := launched(t, s)
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

// Integration review, Important: the permission words are the admitted
// process's own. The run's argv starts with the process's launch argv and
// adds none after it; a spec whose words differ from the admitted ones is
// refused, and its argv still holds only what was admitted.
func TestArgvTakesLaunchWordsFromTheProcess(t *testing.T) {
	s := spec(t, "codex")
	s.Route.Sandbox, s.Mode, s.Native = "workspace-write", mode.Edits, "workspace-write"
	s.Network, s.WritableRoots, s.ThreadID = true, []string{"/tmp/a"}, "thread-123"
	s = rebuilt(t, s)
	c := spec(t, "claude")
	c.Route.Sandbox, c.Mode, c.Native = "workspace-write", mode.Full, "bypassPermissions"
	c = rebuilt(t, c)
	for _, tc := range []struct {
		s    Spec
		flag string
	}{{s, "-s"}, {c, "--permission-mode"}} {
		a, admitted := Argv(tc.s), tc.s.Process.LaunchArgv()
		if len(tc.s.Process.Launch) == 0 || len(a) < len(admitted) || !slices.Equal(a[:len(admitted)], admitted) {
			t.Fatalf("%s: argv %v does not start with the admitted %v", tc.s.Route.Agent, a, admitted)
		}
		if n := strings.Count(" "+strings.Join(a, " ")+" ", " "+tc.flag+" "); n != 1 {
			t.Errorf("%s: %d permission flags in %v", tc.s.Route.Agent, n, a)
		}
		if err := checkProcess(tc.s); err != nil {
			t.Errorf("%s: %v", tc.s.Route.Agent, err)
		}
	}
	if s.Process.Launch[0] != "exec" || slices.Index(Argv(s), "resume") < len(s.Process.LaunchArgv()) {
		t.Errorf("codex order: %v", Argv(s))
	}
	moved := s
	moved.Network = false
	if checkProcess(moved) == nil {
		t.Error("a spec whose permission words differ from the admitted ones was not refused")
	}
	containsArgs(t, Argv(moved), "sandbox_workspace_write.network_access=true")
	if slices.Contains(Argv(moved), "sandbox_workspace_write.network_access=false") {
		t.Errorf("the runner added words after admission: %v", Argv(moved))
	}
}

// Claude review 3: a Claude worker that only the gate holds read-only, at
// level plan or in a read-only role, starts without the built-in tools that
// are not read-only, as a second wall; Write and Edit included, as a worker
// has no report to write. One --disallowedTools value carries them all.
func TestHeldClaudeWorkerDisallowsWriteTools(t *testing.T) {
	for _, tc := range []struct {
		sandbox string
		m       mode.Mode
		held    bool
	}{
		{"read-only", mode.Full, true},
		{"workspace-write", mode.Plan, true},
		{"workspace-write", mode.Edits, false},
	} {
		s := spec(t, "claude")
		s.Route.Sandbox, s.Mode = tc.sandbox, tc.m
		args := launched(t, s)
		var values []string
		for _, a := range args {
			if v, ok := strings.CutPrefix(a, "--disallowedTools="); ok {
				values = append(values, v)
			}
		}
		if len(values) != 1 {
			t.Fatalf("%+v: %v", tc, args)
		}
		got := strings.Split(values[0], ",")
		for _, tool := range append([]string{"Agent", "Task"}, mode.HeldDisallowedTools(false)...) {
			if want := tool == "Agent" || tool == "Task" || tc.held; slices.Contains(got, tool) != want {
				t.Errorf("%+v: %s in %v", tc, tool, got)
			}
		}
	}
}

// A looser mode keeps every setting the gate depends on.
func TestLooserArgvKeepsGateSettings(t *testing.T) {
	s := spec(t, "claude")
	s.Route.Sandbox, s.Mode, s.Native = "workspace-write", mode.Full, "bypassPermissions"
	a := launched(t, s)
	line := strings.Join(a, " ")
	if strings.Contains(line, "--setting-sources") || strings.Contains(line, "disableAllHooks") || !slices.Contains(a, "--settings") {
		t.Fatal(line)
	}
	containsArgs(t, a, "--permission-mode", "bypassPermissions", "--disallowedTools=Agent,Task")
}
