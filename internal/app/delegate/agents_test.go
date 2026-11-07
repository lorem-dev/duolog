package delegate

import (
	"context"

	"github.com/lorem-dev/duolog/internal/app/launch/launchtest"
	"github.com/lorem-dev/duolog/internal/app/quota/quotatest"
	"github.com/lorem-dev/duolog/internal/core/caps"
)

// testAgents are the registered agents with their real trust readers and
// capabilities, which read the temporary homes the tests set, and a fake quota
// reader each: the harness's gate reads on its own.
func testAgents() map[string]Agent {
	out := map[string]Agent{}
	all := launchtest.AllCaps()
	for name, t := range launchtest.Trusts() {
		out[name] = Agent{Trust: t, Caps: all[name], Quota: &quotatest.Fake{Agent: name}}
	}
	return out
}

// scanned is an agent's Capabilities whose Scan gives a fixed inventory; its
// servers and bridge stay the agent's own.
type scanned struct {
	Capabilities
	scan func(environ []string) (caps.Inventory, error)
}

func (s scanned) Scan(_ context.Context, _ string, environ []string) (caps.Inventory, error) {
	return s.scan(environ)
}

// withScan gives each agent of d a Scan of scan, keeping the rest of its
// Capabilities.
func withScan(d *Deps, scan func(agent string, environ []string) (caps.Inventory, error)) {
	for name, a := range d.Agents {
		inner := a.Caps
		if s, ok := inner.(scanned); ok {
			inner = s.Capabilities
		}
		a.Caps = scanned{Capabilities: inner, scan: func(environ []string) (caps.Inventory, error) { return scan(name, environ) }}
		d.Agents[name] = a
	}
}
