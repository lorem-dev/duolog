package delegate

import (
	"github.com/lorem-dev/duolog/internal/app/launch/launchtest"
	"github.com/lorem-dev/duolog/internal/app/quota/quotatest"
)

// testAgents are the registered agents with their real trust readers, which
// read the temporary homes the tests set, and a fake quota reader each: the
// harness's gate reads on its own.
func testAgents() map[string]Agent {
	out := map[string]Agent{}
	for name, t := range launchtest.Trusts() {
		out[name] = Agent{Trust: t, Quota: &quotatest.Fake{Agent: name}}
	}
	return out
}
