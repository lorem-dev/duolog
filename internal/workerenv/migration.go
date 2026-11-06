package workerenv

import (
	"runtime"

	"github.com/lorem-dev/duolog/internal/core/config"
)

// DroppedKeys lists, per agent, the route names that the agent's workers no
// longer get: set in duolog's own environment, even empty, or listed in
// env_passthrough, and still withheld by the variant the agent can use. That
// is the free variant, or the paid one when paid_runs.<agent>.allow is true.
// It is Build's Dropped, so a listed key that paid_runs does not allow
// counts, and the person learns why it did not reach the worker. It compares
// names only; the one value it reads is a Claude base URL, against the
// subscription endpoints.
func DroppedKeys(cfg *config.Config, environ []string) map[string][]string {
	out := map[string][]string{}
	for _, agent := range []string{"claude", "codex"} {
		paid := cfg.PaidRuns.Agent(agent)
		names := cfg.EnvPassthrough
		if paid.Allow {
			names = append(append([]string{}, names...), paid.Env...)
		}
		sel := envSelection{agent: agent, goos: runtime.GOOS, environ: environ, endpoints: endpoints(cfg, agent)}
		sel.pass(names, paid.Allow)
		if len(sel.dropped) > 0 {
			out[agent] = sortedUnique(sel.dropped)
		}
	}
	return out
}

// MigrationLine tells the lead that an agent's workers no longer get name,
// and how to let them: the agent's paid_runs opt-in with a daily cap and the
// key in its paid env. The paid env adds to env_passthrough, so the person
// keeps the default names without writing them out.
func MigrationLine(agent, name string) string {
	key := "paid_runs." + agent
	return agent + " workers no longer get " + name + ". To let them use it, set " + key +
		".allow: true with a daily_cap_usd above 0 and list the key in " + key + ".env."
}
