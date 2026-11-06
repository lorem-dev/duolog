package load

import (
	"reflect"
	"testing"
)

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
	got := Build(peers, limits)
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
	got := Build([]Peer{{ID: "peer-aa", Agent: "claude", Live: true, Idle: true}}, map[string]Limits{"codex": {Limit: 3}})
	if len(got.Agents) != 1 || got.Agents[0].Name != "codex" || got.WindowKnown || len(got.Idle) != 0 {
		t.Fatalf("got %+v", got)
	}
	if got.Line(true) != "duolog peers: codex 0 of 3 - 3 codex sessions free: start independent work or stack it on open pull request branches (duolog peer queue add)" {
		t.Fatalf("line %q", got.Line(true))
	}
}
