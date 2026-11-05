package peer

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/router"
)

// A Claude peer pane starts with fast mode off and keeps its notification
// setting, its state deny list and its tool restrictions.
func TestPeerFastModePinned(t *testing.T) {
	root := t.TempDir()
	for _, context := range []string{"minimal", "project"} {
		args := agentArgv(router.Route{Target: config.Target{Agent: "claude", Model: "opus"}, Context: context}, mode.Ask, root)
		i := slices.Index(args, "--settings")
		if i < 0 || i+1 == len(args) {
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
		if settings.FastMode == nil || *settings.FastMode {
			t.Fatal(context, args[i+1])
		}
		if settings.PreferredNotifChannel != "notifications_disabled" || len(settings.Sandbox.Filesystem.DenyRead) == 0 {
			t.Fatal(context, args[i+1])
		}
		if !slices.Contains(args, "--disallowedTools=Agent,Task") {
			t.Fatal(args)
		}
	}
}
