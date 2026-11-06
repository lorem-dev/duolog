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
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// A peer cannot delegate, so a Claude peer starts without the subagent tools.
func TestClaudePeerDisallowsSubagents(t *testing.T) {
	route := router.Route{Target: config.Target{Agent: "claude", Model: "opus"}}
	if args := agentArgv(route, mode.Ask, peerProcess(t, &config.Config{}, route, workerenv.Free, t.TempDir())); !slices.Contains(args, "--disallowedTools=Agent,Task") {
		t.Fatal(args)
	}
	route.Agent, route.Model = "codex", "sol"
	for _, a := range agentArgv(route, mode.Ask, peerProcess(t, &config.Config{}, route, workerenv.Free, t.TempDir())) {
		if strings.Contains(a, "disallowedTools") || strings.Contains(a, "Agent") {
			t.Fatal(a)
		}
	}
}

func TestClaudePeerSettingsDenyStateAndKeepNotifications(t *testing.T) {
	root := t.TempDir()
	for _, context := range []string{"minimal", "project"} {
		route := router.Route{Target: config.Target{Agent: "claude", Model: "opus"}, Context: context}
		args := agentArgv(route, mode.Ask, peerProcess(t, &config.Config{}, route, workerenv.Free, root))
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
