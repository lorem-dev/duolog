package peer

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/router"
)

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
