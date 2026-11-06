package kit

import (
	"slices"

	"github.com/lorem-dev/duolog/internal/core/launch"
)

// AgentArgv is the command and environment of an agent's own check: the
// worker's process when p is a process of agent, so its prefix, settings and
// exactly its environment; else the plain command in duolog's environment,
// which is a nil env.
func AgentArgv(p launch.Process, agent string, args ...string) (argv, env []string) {
	if p.Agent == agent {
		return p.Argv(args...), slices.Clone(p.Env)
	}
	return append([]string{agent}, args...), nil
}
