package trust

import "testing"

func TestGateProblem(t *testing.T) {
	for _, tc := range []struct {
		agent  string
		s      State
		folder bool
		want   string
	}{
		{"claude", State{Trusted: true, Installed: true}, true, ""},
		{"claude", State{Trusted: true, Installed: true, HooksOff: "/x/settings.json"}, true, "/x/settings.json sets disableAllHooks"},
		{"claude", State{Trusted: true}, true, "Claude has no duolog gate here"},
		{"codex", State{Trusted: true}, true, ""},
		{"codex", State{}, true, "Codex hooks are not approved; open /hooks"},
		{"codex", State{Trusted: true}, false, "Codex does not trust this folder"},
	} {
		if got := GateProblem(tc.agent, tc.s, tc.folder); got != tc.want {
			t.Errorf("%s %+v %t: %q want %q", tc.agent, tc.s, tc.folder, got, tc.want)
		}
	}
}
