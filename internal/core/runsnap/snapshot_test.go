package runsnap

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLineCountsLivePeersRunningRunsAsksQueue(t *testing.T) {
	s := Snapshot{
		Peers:    []Peer{{State: "ready"}, {State: "question"}, {State: "exited"}},
		Runs:     []Run{{State: "running"}, {State: "done"}},
		AsksOpen: 1,
	}
	if got := s.Line(); got != "duolog 2 peers 1 run 1 ask" {
		t.Fatalf("line %q", got)
	}
	s.Load.Queued = 3
	if got := s.Line(); !strings.HasSuffix(got, " 3 queued") {
		t.Fatalf("line %q, want it to end with the queue", got)
	}
}

func TestLineEmpty(t *testing.T) {
	for _, s := range []Snapshot{
		{},
		{Peers: []Peer{{State: "exited"}}, Runs: []Run{{State: "done"}}},
	} {
		if got := s.Line(); got != "" {
			t.Fatalf("line %q for %+v, want none", got, s)
		}
	}
}

func TestEmptySnapshotJSON(t *testing.T) {
	data, err := json.Marshal(Snapshot{Version: 1, Peers: []Peer{}, Runs: []Run{}, Load: Load{Agents: []AgentLoad{}}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"version":1,"generated_at":"","line":"","lead":null,"rex":{"available":false},"peers":[],"runs":[],"asks_open":0,"load":{"agents":[],"queued":0,"window_5h":null,"weekly":null}}`
	if string(data) != want {
		t.Fatalf("json =\n%s\nwant\n%s", data, want)
	}
}
