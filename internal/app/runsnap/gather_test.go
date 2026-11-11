package runsnap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/load"
	"github.com/lorem-dev/duolog/internal/app/peer"
	"github.com/lorem-dev/duolog/internal/core/config"
)

// fakeDeps is Deps on a real registry in a temp folder, with ports that
// record their calls.
type fakeDeps struct {
	deps      Deps
	records   [][2]string
	peersLead []string
	loads     int
}

func newFake(t *testing.T, withLead bool) *fakeDeps {
	t.Helper()
	t.Setenv("DUOLOG_LEAD", "")
	reg, err := session.Open(filepath.Join(t.TempDir(), ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if withLead {
		if err := reg.RegisterLead(session.Lead{ID: "lead-1", Target: config.Target{Agent: "codex"}, PID: os.Getpid()}); err != nil {
			t.Fatal(err)
		}
	}
	f := &fakeDeps{}
	f.deps = Deps{
		Registry: reg,
		Peers: func(_ context.Context, leadID string, _ bool) ([]peer.ListedPeer, error) {
			f.peersLead = append(f.peersLead, leadID)
			return []peer.ListedPeer{{ID: "peer-1", Agent: "claude", State: peer.Ready}}, nil
		},
		Records: func(leadID, address string) ([]delegate.LocatedRecord, error) {
			f.records = append(f.records, [2]string{leadID, address})
			return []delegate.LocatedRecord{{Record: delegate.Record{ID: "d-1"}}, {Record: delegate.Record{ID: "d-2"}}}, nil
		},
		Load: func(context.Context, string, []peer.ListedPeer) load.Summary {
			f.loads++
			return load.Summary{Queued: 1}
		},
	}
	return f
}

func TestGatherNoLead(t *testing.T) {
	f := newFake(t, false)
	g, err := Gather(context.Background(), f.deps, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if g.Lead != nil || g.LeadErr == nil {
		t.Fatalf("lead %+v, err %v", g.Lead, g.LeadErr)
	}
	if !slices.Equal(f.records, [][2]string{{"", ""}}) {
		t.Fatalf("records called with %v, want once with no lead", f.records)
	}
	if want := filepath.Dir(f.deps.Registry.Dir); g.Root != want {
		t.Fatalf("root %q, want %q", g.Root, want)
	}
}

func TestGatherWithLead(t *testing.T) {
	f := newFake(t, true)
	g, err := Gather(context.Background(), f.deps, Options{LeadID: "lead-1"})
	if err != nil {
		t.Fatal(err)
	}
	if g.Lead == nil || g.Lead.ID != "lead-1" || g.Address != "codex/lead.lead-1" {
		t.Fatalf("lead %+v, address %q", g.Lead, g.Address)
	}
	if !slices.Equal(f.records, [][2]string{{"lead-1", "codex/lead.lead-1"}}) || !slices.Equal(f.peersLead, []string{"lead-1"}) {
		t.Fatalf("records %v, peers %v", f.records, f.peersLead)
	}
	if !g.Rex || len(g.Peers) != 1 || f.loads != 1 || g.Load.Queued != 1 {
		t.Fatalf("rex %v, peers %v, loads %d, load %+v", g.Rex, g.Peers, f.loads, g.Load)
	}
}

func TestGatherWithoutALoadPortComputesNoLoad(t *testing.T) {
	f := newFake(t, true)
	f.deps.Load = nil
	g, err := Gather(context.Background(), f.deps, Options{LeadID: "lead-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !g.Rex || len(g.Peers) != 1 || g.Load.Queued != 0 {
		t.Fatalf("rex %v, peers %v, load %+v", g.Rex, g.Peers, g.Load)
	}
}

func TestGatherNoRex(t *testing.T) {
	f := newFake(t, true)
	f.deps.Peers = func(context.Context, string, bool) ([]peer.ListedPeer, error) { return nil, peer.ErrNoRex }
	g, err := Gather(context.Background(), f.deps, Options{LeadID: "lead-1"})
	if err != nil {
		t.Fatal(err)
	}
	if g.Rex || g.PeersErr != nil || f.loads != 0 {
		t.Fatalf("rex %v, peers err %v, %d loads", g.Rex, g.PeersErr, f.loads)
	}
}

func TestGatherPeerErrorKeepsTheRest(t *testing.T) {
	f := newFake(t, true)
	f.deps.Peers = func(context.Context, string, bool) ([]peer.ListedPeer, error) { return nil, errors.New("x") }
	g, err := Gather(context.Background(), f.deps, Options{LeadID: "lead-1"})
	if err != nil {
		t.Fatal(err)
	}
	if g.PeersErr == nil || g.PeersErr.Error() != "x" || len(g.Records) != 2 || f.loads != 0 {
		t.Fatalf("peers err %v, records %v, %d loads", g.PeersErr, g.Records, f.loads)
	}
}

func TestGatherAsksOnlyToTheLead(t *testing.T) {
	f := newFake(t, true)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var data []byte
	for _, m := range []map[string]any{
		{"n": 1, "t": now, "from": "claude/worker", "to": "codex/lead.lead-1", "k": "ask", "s": "mine", "p": true},
		{"n": 2, "t": now, "from": "claude/worker", "to": "claude/lead.other", "k": "ask", "s": "theirs"},
	} {
		line, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		data = append(append(data, line...), '\n')
	}
	if err := os.WriteFile(filepath.Join(f.deps.Registry.Dir, "bus.ndjson"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	g, err := Gather(context.Background(), f.deps, Options{LeadID: "lead-1"})
	if err != nil {
		t.Fatal(err)
	}
	want := []Ask{{N: 1, From: "claude/worker", Summary: "mine", PersonOnly: true}}
	if !slices.Equal(g.Asks, want) {
		t.Fatalf("asks %+v, want %+v", g.Asks, want)
	}
}

func TestGatherStopsOnADoneContext(t *testing.T) {
	f := newFake(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Gather(ctx, f.deps, Options{LeadID: "lead-1"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v, want canceled", err)
	}
}

func TestShownRunsRunningThenFiveFinished(t *testing.T) {
	var in []delegate.LocatedRecord
	add := func(id, state string) {
		in = append(in, delegate.LocatedRecord{Record: delegate.Record{ID: id, State: state}})
	}
	for i := range 7 {
		add(fmt.Sprintf("d-done%d", i), "done")
		if i == 1 || i == 4 {
			add(fmt.Sprintf("d-run%d", i), "running")
		}
	}
	var ids []string
	for _, r := range ShownRuns(in) {
		ids = append(ids, r.ID)
	}
	want := []string{"d-run1", "d-run4", "d-done0", "d-done1", "d-done2", "d-done3", "d-done4"}
	if !slices.Equal(ids, want) || Finished != 5 {
		t.Fatalf("ids %v, want %v", ids, want)
	}
}
