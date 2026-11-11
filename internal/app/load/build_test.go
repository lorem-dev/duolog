package load

import (
	"reflect"
	"testing"
)

// agents are the agents the tests' lines cover, in the registry's order.
var agents = []string{"claude", "codex"}

func TestBuildCountsLivePeersAndIdleOnes(t *testing.T) {
	peers := []Peer{
		{ID: "peer-ab12cd", Agent: "claude", Live: true, Idle: true},
		{ID: "peer-cd34ef", Agent: "claude", Live: true},
		{ID: "peer-ef56ab", Agent: "claude", Live: true},
		{ID: "peer-aa0000", Agent: "claude"},
		{ID: "peer-cc2222", Agent: "codex", Live: true, Idle: true},
	}
	limits := map[string]Limits{
		"claude": {Limit: 12, Live: 5, Window: 41, WindowKnown: true},
		"codex":  {Limit: 3, Live: 1, Window: 12, WindowKnown: true},
	}
	got := Build(agents, peers, limits)
	want := Summary{
		Agents: []Agent{
			{Name: "claude", Running: 3, Idle: 1, Limit: 12, Live: 5},
			{Name: "codex", Running: 1, Idle: 1, Limit: 3, Live: 1},
		},
		Idle:   []string{"peer-ab12cd", "peer-cc2222"},
		Window: 41, WindowKnown: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestBuildLeavesOutAgentsWithoutALimit(t *testing.T) {
	got := Build(agents, []Peer{{ID: "peer-aa", Agent: "claude", Live: true, Idle: true}}, map[string]Limits{"codex": {Limit: 3}})
	if len(got.Agents) != 1 || got.Agents[0].Name != "codex" || got.WindowKnown || len(got.Idle) != 0 {
		t.Fatalf("got %+v", got)
	}
	// The window is unknown, so the line advises no more work.
	if got.Line() != "duolog peers: codex 0 of 3" {
		t.Fatalf("line %q", got.Line())
	}
}

func TestBuildKeepsHighestWeekly(t *testing.T) {
	got := Build(agents, nil, map[string]Limits{
		"claude": {Limit: 12, Weekly: 30, WeeklyKnown: true},
		"codex":  {Limit: 3, Weekly: 55, WeeklyKnown: true},
	})
	if !got.WeeklyKnown || got.Weekly != 55 {
		t.Fatalf("weekly %g known %v, want 55", got.Weekly, got.WeeklyKnown)
	}
	got = Build(agents, nil, map[string]Limits{"claude": {Limit: 12}, "codex": {Limit: 3}})
	if got.WeeklyKnown || got.Weekly != 0 {
		t.Fatalf("weekly %g known %v, want unknown", got.Weekly, got.WeeklyKnown)
	}
}
