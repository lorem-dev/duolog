package install

import (
	"context"
	"os"
	"time"

	plugininstall "github.com/lorem-dev/duolog/internal/core/agentplugin"
)

// PluginState is implemented by an installer that can read whether its
// duolog plugin is installed without starting an agent CLI, and that names
// the CLI arguments which add the marketplace and then install the plugin.
type PluginState interface {
	Installed(root, home string, environ []string) bool
	PluginCommands() [][]string
}

// PluginNote is the missing-plugin note of agent a, or "" when its installer
// has no plugin.
func PluginNote(a Agent) string {
	state, ok := a.Installer.(PluginState)
	if !ok {
		return ""
	}
	return plugininstall.PluginMissingNote(a.Name, state.PluginCommands())
}

// pluginTimeout bounds each marketplace or plugin command separately.
const pluginTimeout = 60 * time.Second

// pluginMissing reports an available agent whose duolog plugin is absent.
// A nil availability lookup or an installer without plugin state skips it.
func (d Deps) pluginMissing(a Agent, o Options, base string) bool {
	state, ok := a.Installer.(PluginState)
	return ok && d.Available != nil && d.Available(a.Name) && !state.Installed(base, o.Home, d.pluginEnviron())
}

// pluginEnviron selects the environment of the agent commands and state.
func (d Deps) pluginEnviron() []string {
	if d.Environ != nil {
		return d.Environ
	}
	return os.Environ()
}

// installPlugins installs each missing plugin before native hooks are planned,
// so the existing Enabled check reads the state written by the agent CLI.
func (d Deps) installPlugins(agents []Agent, o Options, base string) []string {
	if o.NoPlugin {
		return nil
	}
	var notes []string
	for _, a := range agents {
		if !d.pluginMissing(a, o, base) {
			continue
		}
		if !d.Release || d.RunPlugin == nil || !d.installPlugin(a, o.Root) {
			notes = append(notes, PluginNote(a))
		}
	}
	return notes
}

// installPlugin lists the marketplaces, adds duolog when absent and installs
// the plugin of agent a. A failed command stops only this agent's plugin
// setup.
func (d Deps) installPlugin(a Agent, root string) bool {
	state, ok := a.Installer.(PluginState)
	if !ok {
		return false
	}
	list, err := d.pluginCommand(root, a.Name, "plugin", "marketplace", "list")
	if err != nil {
		return false
	}
	commands := state.PluginCommands()
	if !plugininstall.HasMarketplace(list, "duolog") {
		if _, err := d.pluginCommand(root, a.Name, commands[0]...); err != nil {
			return false
		}
	}
	_, err = d.pluginCommand(root, a.Name, commands[1]...)
	return err == nil
}

// pluginCommand gives one injected runner call its own one-minute deadline.
func (d Deps) pluginCommand(root, agent string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pluginTimeout)
	defer cancel()
	return d.RunPlugin(ctx, root, d.pluginEnviron(), agent, args...)
}
