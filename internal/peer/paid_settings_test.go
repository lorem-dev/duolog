package peer

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// peerProcess builds the process of a peer pane for route.
func peerProcess(t *testing.T, cfg *config.Config, route coreroute.Route, variant workerenv.Variant, stateDir string, roots ...string) workerenv.Process {
	t.Helper()
	return peerProcessIn(t, cfg, route, mode.Ask, variant, stateDir, roots...)
}

// peerProcessIn builds the pane of route under a lead in mode m.
func peerProcessIn(t *testing.T, cfg *config.Config, route coreroute.Route, m mode.Mode, variant workerenv.Variant, stateDir string, roots ...string) workerenv.Process {
	t.Helper()
	p, err := agentEnv(peerInput(cfg, route, m, variant, "lead", "plan", "peer-test", "", stateDir, roots, os.Environ()))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// A Claude peer pane starts with fast mode off in the free variant and keeps
// its notification setting, its state deny list and its tool restrictions.
// The paid variant leaves fast mode to the person's settings. The settings
// come once, from the built process.
func TestPeerFastModePinned(t *testing.T) {
	root := t.TempDir()
	paid := &config.Config{}
	paid.PaidRuns.Claude.Allow, paid.PaidRuns.Claude.DailyCapUSD = true, 5
	for _, variant := range []workerenv.Variant{workerenv.Free, workerenv.Paid} {
		for _, context := range []string{"minimal", "project"} {
			route := coreroute.Route{Target: config.Target{Agent: "claude", Model: "opus"}, Context: context}
			args := agentArgv(route, mode.Ask, peerProcess(t, paid, route, variant, root))
			i := slices.Index(args, "--settings")
			if i != 1 || slices.Index(args[i+1:], "--settings") >= 0 {
				t.Fatal(args)
			}
			var settings struct {
				FastMode              *bool  `json:"fastMode"`
				PreferredNotifChannel string `json:"preferredNotifChannel"`
				Sandbox               struct {
					Filesystem struct {
						DenyRead []string `json:"denyRead"`
					} `json:"filesystem"`
				} `json:"sandbox"`
			}
			if err := json.Unmarshal([]byte(args[i+1]), &settings); err != nil {
				t.Fatal(err)
			}
			if variant == workerenv.Free && (settings.FastMode == nil || *settings.FastMode) || variant == workerenv.Paid && settings.FastMode != nil {
				t.Fatal(variant, context, args[i+1])
			}
			if settings.PreferredNotifChannel != "notifications_disabled" || len(settings.Sandbox.Filesystem.DenyRead) == 0 {
				t.Fatal(context, args[i+1])
			}
			if !slices.Contains(args, "--disallowedTools=Agent,Task") {
				t.Fatal(args)
			}
		}
	}
}
