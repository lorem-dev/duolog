package delegate

import "github.com/lorem-dev/duolog/internal/app/launch/launchtest"

// testAgents are the registered agents with their real trust readers, which
// read the temporary homes the tests set.
func testAgents() map[string]Agent {
	out := map[string]Agent{}
	for name, t := range launchtest.Trusts() {
		out[name] = Agent{Trust: t}
	}
	return out
}
