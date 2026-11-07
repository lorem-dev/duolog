package peer

import (
	"github.com/lorem-dev/duolog/internal/app/peer/panetest"
	"github.com/lorem-dev/duolog/internal/core/agent"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/mode"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	corescreen "github.com/lorem-dev/duolog/internal/core/screen"
)

// testPanes is the real pane support of both agents: the peer tests assert
// what a real pane's argv and texts do.
func testPanes() map[string]PaneSupport {
	out := map[string]PaneSupport{}
	for name, p := range panetest.Panes() {
		out[name] = PaneSupport{Agent: p.Agent, Facts: p.Facts}
	}
	return out
}

func testPane(agent string) PaneSupport { return testPanes()[agent] }

// argvFor is the argv of route's pane under a lead in mode m.
func argvFor(route coreroute.Route, m mode.Mode, p corelaunch.Process) []string {
	return testPane(route.Agent).Agent.Argv(route, m, p)
}

// settingsFor are the extra settings route's agent adds to its pane.
func settingsFor(route coreroute.Route) map[string]any {
	return testPane(route.Agent).Agent.Settings(agent.PaneStart{})
}

// parseFor reads a screen with the pane facts of the named agent.
func parseFor(agent string, raw []byte) Screen {
	return corescreen.Parse(raw, testPane(agent).Facts)
}

// readPluginOffer is Claude's reading of an offer that names its plugin: an
// install question without its metadata is no offer here.
func readPluginOffer(s Screen) (corescreen.Offer, bool) {
	offer, ok := testPane("claude").Agent.Offer(s)
	return offer, ok && offer.Plugin != ""
}

func installQuestion(s Screen) bool { return corescreen.InstallQuestion(s) }
