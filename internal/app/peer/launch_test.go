package peer

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// A peer cannot delegate, so a Claude peer starts without the subagent tools.
func TestClaudePeerDisallowsSubagents(t *testing.T) {
	route := coreroute.Route{Target: config.Target{Agent: "claude", Model: "opus"}}
	if args := agentArgv(route, peerProcess(t, &config.Config{}, route, workerenv.Free, t.TempDir())); !slices.Contains(args, "--disallowedTools=Agent,Task") {
		t.Fatal(args)
	}
	route.Agent, route.Model = "codex", "sol"
	for _, a := range agentArgv(route, peerProcess(t, &config.Config{}, route, workerenv.Free, t.TempDir())) {
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
	paid.PaidRuns.Codex.Allow, paid.PaidRuns.Codex.DailyCapUSD = true, 5
	pinned := 0
	for _, variant := range []workerenv.Variant{workerenv.Free, workerenv.Paid} {
		for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Edits, mode.Full} {
			for _, sandbox := range []string{"read-only", "workspace-write"} {
				route := coreroute.Route{Target: config.Target{Agent: "codex", Model: "sol"}, Sandbox: sandbox}
				p := peerProcessIn(t, paid, route, m, variant, t.TempDir())
				args := agentArgv(route, p)
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
	paid.PaidRuns.Codex.Allow, paid.PaidRuns.Codex.DailyCapUSD = true, 5
	paid.PaidRuns.Claude.Allow, paid.PaidRuns.Claude.DailyCapUSD = true, 5
	for _, variant := range []workerenv.Variant{workerenv.Free, workerenv.Paid} {
		for _, tc := range []struct {
			agent, model, sandbox string
			m                     mode.Mode
			want                  []string
			flag                  string
		}{
			{"codex", "sol", "workspace-write", mode.Edits, []string{"-s", "workspace-write", "-c", "sandbox_workspace_write.network_access=false", "-c", "sandbox_workspace_write.writable_roots=[]"}, "-s"},
			{"codex", "sol", "read-only", mode.Full, []string{"-s", "read-only"}, "-s"},
			{"claude", "opus", "workspace-write", mode.Edits, []string{"--permission-mode", "acceptEdits"}, "--permission-mode"},
		} {
			route := coreroute.Route{Target: config.Target{Agent: tc.agent, Model: tc.model}, Sandbox: tc.sandbox}
			p := peerProcessIn(t, paid, route, tc.m, variant, t.TempDir())
			if !slices.Equal(p.Launch, tc.want) {
				t.Errorf("%s %s: admitted launch %v, want %v", variant, tc.agent, p.Launch, tc.want)
			}
			args, admitted := agentArgv(route, p), p.LaunchArgv()
			if len(args) < len(admitted) || !slices.Equal(args[:len(admitted)], admitted) || slices.Contains(args[len(admitted):], tc.flag) {
				t.Errorf("%s %s: launched %v, admitted %v", variant, tc.agent, args, admitted)
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
		args := agentArgv(route, peerProcess(t, &config.Config{}, route, workerenv.Free, root))
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
