package peer

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/mode"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
)

// A peer cannot delegate, so a Claude peer starts without the subagent tools.
func TestClaudePeerDisallowsSubagents(t *testing.T) {
	route := coreroute.Route{Target: config.Target{Agent: "claude", Model: "opus"}}
	if args := argvFor(route, mode.Ask, peerProcess(t, &config.Config{}, route, corelaunch.Free, t.TempDir())); !slices.Contains(args, "--disallowedTools=Agent,Task") {
		t.Fatal(args)
	}
	route.Agent, route.Model = "codex", "sol"
	for _, a := range argvFor(route, mode.Plan, peerProcess(t, &config.Config{}, route, corelaunch.Free, t.TempDir())) {
		if strings.Contains(a, "disallowedTools") || strings.Contains(a, "Agent") {
			t.Fatal(a)
		}
	}
}

// Re-review m4: a cloned repository's .codex/config.toml cannot widen a Codex
// peer's workspace-write sandbox, as it cannot a worker's. The pins follow
// the built process's prefix in the free and the paid variant alike.
func TestCodexPeerPinsSandboxExtras(t *testing.T) {
	paid := &config.Config{}
	pa := paid.PaidRuns.Agent("codex")
	pa.Allow, pa.DailyCapUSD = true, 5
	paid.PaidRuns.Set("codex", pa)
	pinned := 0
	for _, variant := range []corelaunch.Variant{corelaunch.Free, corelaunch.Paid} {
		for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Edits, mode.Full} {
			for _, sandbox := range []string{"read-only", "workspace-write"} {
				route := coreroute.Route{Target: config.Target{Agent: "codex", Model: "sol"}, Sandbox: sandbox}
				p := peerProcessIn(t, paid, route, m, variant, t.TempDir())
				args := argvFor(route, m, p)
				if !slices.Equal(args[:len(p.Prefix)], p.Prefix) {
					t.Fatalf("%s %s %s: argv does not start with the built prefix: %v", variant, m, sandbox, args)
				}
				checkCodexPeerPins(t, args, m, sandbox, &pinned)
			}
		}
	}
	if pinned == 0 {
		t.Fatal("no workspace-write peer was checked")
	}
}

// Integration review, Important: a pane's permission flags and Codex's pins
// are in the process the gate admits, and the pane's argv is that process's
// launch argv with nothing permission-shaped added after it, free and paid.
func TestPeerAdmittedArgvIsLaunchedArgv(t *testing.T) {
	paid := &config.Config{}
	for _, name := range []string{"codex", "claude"} {
		pa := paid.PaidRuns.Agent(name)
		pa.Allow, pa.DailyCapUSD = true, 5
		paid.PaidRuns.Set(name, pa)
	}
	for _, variant := range []corelaunch.Variant{corelaunch.Free, corelaunch.Paid} {
		for _, tc := range []struct {
			agent, model, sandbox string
			m                     mode.Mode
			want                  []string
			flag                  string
		}{
			{"codex", "sol", "workspace-write", mode.Edits, []string{"-s", "workspace-write", "-c", "sandbox_workspace_write.network_access=false", "-c", "sandbox_workspace_write.writable_roots=[]"}, "-s"},
			{"codex", "sol", "read-only", mode.Full, []string{"-s", "read-only"}, "-s"},
			{"claude", "opus", "workspace-write", mode.Edits, []string{"--permission-mode", "acceptEdits"}, "--permission-mode"},
			{"claude", "opus", "read-only", mode.Full, []string{"--permission-mode", "default"}, "--permission-mode"},
		} {
			route := coreroute.Route{Target: config.Target{Agent: tc.agent, Model: tc.model}, Sandbox: tc.sandbox}
			p := peerProcessIn(t, paid, route, tc.m, variant, t.TempDir())
			if !slices.Equal(p.Launch, tc.want) {
				t.Errorf("%s %s: admitted launch %v, want %v", variant, tc.agent, p.Launch, tc.want)
			}
			args, admitted := argvFor(route, tc.m, p), p.LaunchArgv()
			if len(args) < len(admitted) || !slices.Equal(args[:len(admitted)], admitted) || slices.Contains(args[len(admitted):], tc.flag) {
				t.Errorf("%s %s: launched %v, admitted %v", variant, tc.agent, args, admitted)
			}
		}
	}
}

// A Claude peer never starts in Claude's plan mode: plan's own "Exit plan
// mode?" prompt waits for a person the pane does not have. A read-only role
// or a lead in plan gives the pane Claude's default mode, and the gate keeps
// it read-only.
func TestClaudePeerNeverLaunchesInPlan(t *testing.T) {
	for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Full} {
		for _, sandbox := range []string{"read-only", "workspace-write"} {
			route := coreroute.Route{Target: config.Target{Agent: "claude", Model: "opus"}, Sandbox: sandbox}
			p := peerProcessIn(t, &config.Config{}, route, m, corelaunch.Free, t.TempDir())
			args := argvFor(route, m, p)
			if slices.Contains(args, "plan") || strings.Contains(p.Settings, `"plan"`) {
				t.Errorf("%s %s: %v", m, sandbox, args)
			}
			if (m == mode.Plan || sandbox == "read-only") && !slices.Equal(p.Launch, []string{"--permission-mode", "default"}) {
				t.Errorf("%s %s: launch %v", m, sandbox, p.Launch)
			}
		}
	}
}

// Claude review 3: a Claude peer that only the gate holds read-only, under a
// lead in plan or in a read-only role, starts without the built-in tools that
// are not read-only, beside Agent and Task. It keeps Write and Edit for its
// own report, which the gate allows. One --disallowedTools value holds them.
func TestHeldClaudePeerDisallowsWriteTools(t *testing.T) {
	for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Full} {
		for _, sandbox := range []string{"read-only", "workspace-write"} {
			route := coreroute.Route{Target: config.Target{Agent: "claude", Model: "opus"}, Sandbox: sandbox}
			args := argvFor(route, m, peerProcessIn(t, &config.Config{}, route, m, corelaunch.Free, t.TempDir()))
			var values []string
			for _, a := range args {
				if v, ok := strings.CutPrefix(a, "--disallowedTools="); ok {
					values = append(values, v)
				}
			}
			if len(values) != 1 {
				t.Fatalf("%s %s: %v", m, sandbox, args)
			}
			got := strings.Split(values[0], ",")
			held := m == mode.Plan || sandbox == "read-only"
			for _, tool := range mode.HeldDisallowedTools(true) {
				if slices.Contains(got, tool) != held {
					t.Errorf("%s %s: %s in %v", m, sandbox, tool, got)
				}
			}
			if !slices.Contains(got, "Agent") || !slices.Contains(got, "Task") || slices.Contains(got, "Write") || slices.Contains(got, "Edit") {
				t.Errorf("%s %s: %v", m, sandbox, got)
			}
		}
	}
}

// Claude review 4: a peer opened under a lead in plan keeps level plan when
// the lead later moves to a looser mode. Its pane gets DUOLOG_MODE=plan, which
// the gate takes only to lower. Under any other lead it gets none, and a
// read-only role stays read-only through its role sandbox, not through
// DUOLOG_MODE, so that it may still read the web.
func TestPeerUnderPlanLeadPinsPlan(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Edits, mode.Full} {
			for _, sandbox := range []string{"read-only", "workspace-write"} {
				route := coreroute.Route{Target: config.Target{Agent: agent, Model: map[string]string{"claude": "opus", "codex": "sol"}[agent]}, Sandbox: sandbox}
				env := peerProcessIn(t, &config.Config{}, route, m, corelaunch.Free, t.TempDir()).Env
				if got, want := slices.Contains(env, "DUOLOG_MODE=plan"), m == mode.Plan; got != want {
					t.Errorf("%s %s %s: DUOLOG_MODE=plan %t", agent, m, sandbox, got)
				}
				if slices.ContainsFunc(env, func(v string) bool { return strings.HasPrefix(v, "DUOLOG_MODE=") && v != "DUOLOG_MODE=plan" }) {
					t.Errorf("%s %s %s: %v", agent, m, sandbox, env)
				}
			}
		}
	}
}

func checkCodexPeerPins(t *testing.T, args []string, m mode.Mode, sandbox string, pinned *int) {
	t.Helper()
	i := slices.Index(args, "-s")
	if i < 0 || args[i+1] != "workspace-write" {
		if slices.Contains(args, "sandbox_workspace_write.network_access=false") {
			t.Errorf("%s %s pins without workspace-write: %v", m, sandbox, args)
		}
		return
	}
	*pinned++
	if !slices.Contains(args, "sandbox_workspace_write.network_access=false") || !slices.Contains(args, "sandbox_workspace_write.writable_roots=[]") {
		t.Errorf("%s %s: %v", m, sandbox, args)
	}
}

func TestClaudePeerSettingsDenyStateAndKeepNotifications(t *testing.T) {
	root := t.TempDir()
	for _, context := range []string{"minimal", "project"} {
		route := coreroute.Route{Target: config.Target{Agent: "claude", Model: "opus"}, Context: context}
		args := argvFor(route, mode.Ask, peerProcess(t, &config.Config{}, route, corelaunch.Free, root))
		i := slices.Index(args, "--settings")
		if i < 0 || i+1 == len(args) {
			t.Fatal(args)
		}
		var settings struct {
			PreferredNotifChannel string   `json:"preferredNotifChannel"`
			ClaudeMdExcludes      []string `json:"claudeMdExcludes"`
			Sandbox               struct {
				Enabled    *bool `json:"enabled"`
				Filesystem struct {
					DenyRead []string `json:"denyRead"`
				} `json:"filesystem"`
			} `json:"sandbox"`
		}
		if err := json.Unmarshal([]byte(args[i+1]), &settings); err != nil {
			t.Fatal(err)
		}
		if settings.PreferredNotifChannel != "notifications_disabled" || settings.Sandbox.Enabled != nil {
			t.Fatal(args[i+1])
		}
		if !slices.Contains(settings.Sandbox.Filesystem.DenyRead, filepath.ToSlash(root)+"/.duolog/decisions.ndjson") {
			t.Fatal(args[i+1])
		}
		if (len(settings.ClaudeMdExcludes) > 0) != (context == "minimal") {
			t.Fatal(context, args[i+1])
		}
	}
}
