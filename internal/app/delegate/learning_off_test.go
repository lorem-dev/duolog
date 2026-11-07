package delegate

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
)

// TestLadderSkipsSwitchedOffAgent: a ladder whose first entry runs a
// switched-off agent takes the next entry.
func TestLadderSkipsSwitchedOffAgent(t *testing.T) {
	on, off := true, false
	cfg := &config.Config{
		Agents: map[string]config.AgentSwitch{"claude": {Enabled: &on}, "codex": {Enabled: &off}},
		Learning: config.Learning{Classes: map[string]config.Class{"hard": {
			Roles: []string{"implement"},
			Ladder: []config.LadderEntry{
				{Agent: "codex", Model: "sol", Effort: "high"},
				{Agent: "claude", Model: "sonnet", Effort: "high"},
			},
		}}},
	}
	lr := &learnRun{cfg: cfg}
	lr.rec.Class = "hard"
	var ro coreroute.Options
	if err := lr.route(cfg, "implement", nil, &ro); err != nil {
		t.Fatal(err)
	}
	if ro.Use == nil || ro.Use.Agent != "claude" {
		t.Fatalf("use = %+v, want the claude entry", ro.Use)
	}
}
