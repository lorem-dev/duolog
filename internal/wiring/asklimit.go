package wiring

import (
	"path/filepath"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/app/gate"
	hookfile "github.com/lorem-dev/duolog/internal/install"
	"github.com/lorem-dev/duolog/internal/trusthome"
)

// HookAskLimit is the shortest timeout of the gate hooks that can be running
// for agent and event in the project at root, for a worker in work. A new
// duolog can run under hooks that an earlier install wrote, and an agent that
// kills a hook can let the tool run, so an ask must fit the hook that is really
// there. When no hooks file shows a gate, it is gate.LegacyHookBudget.
func HookAskLimit(agent, event, root, work string, environ []string) time.Duration {
	seconds, ok := hookfile.GateHookSeconds(gateHookFiles(agent, root, work, environ), event)
	if !ok {
		return gate.LegacyHookBudget
	}
	return time.Duration(seconds * float64(time.Second))
}

// gateHookFiles lists every file that can hold agent's gate hooks.
func gateHookFiles(agent, root, work string, environ []string) []string {
	home, err := trusthome.HomeIn(environ)
	if err != nil {
		return nil
	}
	folders := []string{root}
	if work != root {
		folders = append(folders, work)
	}
	var files []string
	switch agent {
	case claude.Facts.Name:
		var i claude.Installer
		for _, folder := range folders {
			files = append(append(files, i.LocalSettings(folder)), i.OtherSettings(folder, home, environ)...)
			if hookfile.PluginEnabledIn(folder, home, environ) {
				files = append(files, hookfile.PluginGateFilesIn(folder, home, environ)...)
			}
		}
	case codex.Facts.Name:
		for _, folder := range folders {
			files = append(files, filepath.Join(folder, ".codex", "hooks.json"))
		}
		if dir, err := kit.Home(environ, "CODEX_HOME", ".codex"); err == nil {
			files = append(files, filepath.Join(dir, "hooks.json"))
		}
	}
	return files
}
