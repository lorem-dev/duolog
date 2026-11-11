package install

import (
	"path/filepath"

	"github.com/lorem-dev/duolog/internal/app/launch/launchtest"
	"github.com/lorem-dev/duolog/internal/core/agent"
)

// InstallOffer is the project instruction that installation writes.
const InstallOffer = agent.InstallOffer

// UsageNote is the usage line that installation writes after the offer, and
// Block is the marked block that holds both.
const (
	UsageNote = agent.UsageNote
	Block     = agent.Block
)

// handler and group are the shape of a hooks document, as the tests read it.
type handler struct {
	Type           string   `json:"type"`
	Command        string   `json:"command"`
	CommandWindows string   `json:"commandWindows,omitempty"`
	Args           []string `json:"args,omitempty"`
	Timeout        int      `json:"timeout,omitempty"`
	AsyncRewake    bool     `json:"asyncRewake,omitempty"`
}

type group struct {
	Hooks []handler `json:"hooks"`
}

// testDeps are the Deps of the registered agents, in order, with their real
// installers.
func testDeps() Deps {
	installers := launchtest.Installers()
	var d Deps
	for _, name := range []string{"claude", "codex"} {
		d.Agents = append(d.Agents, Agent{Name: name, Installer: installers[name]})
	}
	return d
}

// Run and Remove run the Deps methods of the same names on testDeps, so that
// the tests read as they did when these were package functions.
func Run(o Options) (Report, error) { return testDeps().Run(o) }

func Remove(o Options) error { return testDeps().Remove(o) }

// IsOurCommand recognises duolog's own hook commands.
func IsOurCommand(command string) bool { return launchtest.IsOurCommand(command) }

// HooksJSON renders the hooks document of agent as the plugin ships it.
func HooksJSON(agent string, timeout int) ([]byte, error) {
	return launchtest.HooksJSON(agent, timeout)
}

// PluginHooksJSON renders the hooks file the duolog plugin of agent ships.
func PluginHooksJSON(agent string, timeout int) ([]byte, error) {
	return launchtest.PluginHooksJSON(agent, timeout)
}

// installedSkillPath is where the bundled duolog skill of agent goes for o:
// at user scope under o.Home, whatever the scope.
func installedSkillPath(o Options, _, agent string) string {
	return filepath.Join(launchtest.Installers()[agent].SkillDir("user", o.Home, o.Home), "duolog", "SKILL.md")
}
