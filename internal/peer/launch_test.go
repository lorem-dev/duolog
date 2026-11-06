package peer

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/router"
)

// A peer cannot delegate, so a Claude peer starts without the subagent tools.
func TestClaudePeerDisallowsSubagents(t *testing.T) {
	route := router.Route{Target: config.Target{Agent: "claude", Model: "opus"}}
	if args := agentArgv(route, mode.Ask, t.TempDir()); !slices.Contains(args, "--disallowedTools=Agent,Task") {
		t.Fatal(args)
	}
	route.Agent, route.Model = "codex", "sol"
	for _, a := range agentArgv(route, mode.Ask, t.TempDir()) {
		if strings.Contains(a, "disallowedTools") || strings.Contains(a, "Agent") {
			t.Fatal(a)
		}
	}
}

// Re-review m4: a cloned repository's .codex/config.toml cannot widen a Codex
// peer's workspace-write sandbox, as it cannot a worker's.
func TestCodexPeerPinsSandboxExtras(t *testing.T) {
	pinned := 0
	for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Edits, mode.Full} {
		for _, sandbox := range []string{"read-only", "workspace-write"} {
			args := agentArgv(router.Route{Target: config.Target{Agent: "codex", Model: "sol"}, Sandbox: sandbox}, m, t.TempDir())
			i := slices.Index(args, "-s")
			if i < 0 || args[i+1] != "workspace-write" {
				if slices.Contains(args, "sandbox_workspace_write.network_access=false") {
					t.Errorf("%s %s pins without workspace-write: %v", m, sandbox, args)
				}
				continue
			}
			pinned++
			if !slices.Contains(args, "sandbox_workspace_write.network_access=false") || !slices.Contains(args, "sandbox_workspace_write.writable_roots=[]") {
				t.Errorf("%s %s: %v", m, sandbox, args)
			}
		}
	}
	if pinned == 0 {
		t.Fatal("no workspace-write peer was checked")
	}
}

func TestClaudePeerSettingsDenyStateAndKeepNotifications(t *testing.T) {
	root := t.TempDir()
	for _, context := range []string{"minimal", "project"} {
		args := agentArgv(router.Route{Target: config.Target{Agent: "claude", Model: "opus"}, Context: context}, mode.Ask, root)
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
