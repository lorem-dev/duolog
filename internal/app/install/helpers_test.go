package install

import (
	"path/filepath"

	"github.com/lorem-dev/duolog/internal/app/launch/launchtest"
	"github.com/lorem-dev/duolog/internal/core/agent"
)

// InstallOffer is the project instruction that installation writes.
const InstallOffer = agent.InstallOffer

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

// installedSkillPath is where the bundled duolog skill of agent goes for o
// under base.
func installedSkillPath(o Options, base, agent string) string {
	return filepath.Join(launchtest.Installers()[agent].SkillDir(o.Scope, base, o.Home), "duolog", "SKILL.md")
}
