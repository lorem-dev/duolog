package wiring

import (
	"context"
	"os"
	"os/exec"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/app/install"
)

// agentAvailable reports whether the registered agent's CLI is on PATH.
func agentAvailable(agent string) bool {
	_, err := exec.LookPath(agent)
	return err == nil
}

// runPlugin runs one bounded plugin command in the selected project and
// environment. It cancels descendants too and bounds held output pipes.
// With no stdin, permission prompts cannot wait for input.
func runPlugin(ctx context.Context, root string, environ []string, agent string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, agent, args...)
	kit.ProcessTree(cmd)
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir, cmd.Env = root, environ
	return cmd.Output()
}

// pluginMissing reads an agent's duolog plugin state without its CLI, and
// returns the note with the install commands when the plugin is missing.
func pluginMissing(name, root, home string) string {
	a, ok := Find(name)
	if !ok {
		return ""
	}
	state, ok := a.Installer.(install.PluginState)
	if !ok || state.Installed(root, home, os.Environ()) {
		return ""
	}
	return install.PluginNote(install.Agent{Name: name, Installer: a.Installer})
}
