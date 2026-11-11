// Package runsnap gathers what a lead's workers are doing: its live peers,
// its delegate runs, the asks open to it and its load. The MCP status tool
// and duolog runs read the same gathering.
package runsnap

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/gate"
	"github.com/lorem-dev/duolog/internal/app/load"
	"github.com/lorem-dev/duolog/internal/app/peer"
)

// Finished is how many finished runs ShownRuns keeps after the running ones.
const Finished = 5

// Options are what one gathering asks for: the lead (empty: the one that
// resolves), whether peers are read fast, and the time it is read at.
type Options struct {
	LeadID string
	Fast   bool
	Now    time.Time
}

// Deps are the ports of a gathering. Registry is the lead home's registry.
type Deps struct {
	Registry *session.Registry
	// Peers lists the lead's live peers; peer.ErrNoRex when there is no Rex.
	Peers func(ctx context.Context, leadID string, fast bool) ([]peer.ListedPeer, error)
	// Records reads the delegate records of the lead with this id or bus
	// address, newest first; both "" read every record.
	Records func(leadID, address string) ([]delegate.LocatedRecord, error)
	// Load is the lead's load, counted against the listed peers; nil
	// computes none.
	Load func(ctx context.Context, leadID string, peers []peer.ListedPeer) load.Summary
}

// Ask is an open ask to the lead.
type Ask struct {
	N          int64
	From       string
	Summary    string
	PersonOnly bool
}

// Gathered is one gathering. A part that cannot be read keeps its error and
// leaves the other parts as they are.
type Gathered struct {
	// Root is the lead home's project root, absolute.
	Root    string
	Lead    *session.Lead
	LeadErr error
	Address string
	// Rex reports that peers could be asked for: there is a Rex here.
	Rex      bool
	Peers    []peer.ListedPeer
	PeersErr error
	// Records are the lead's delegate records, newest first.
	Records    []delegate.LocatedRecord
	RecordsErr error
	// Asks are the asks open to Address.
	Asks []Ask
	Load load.Summary
}

// Gather reads the lead's workers. It fails only when ctx is done; each part
// it cannot read is left empty with its error.
func Gather(ctx context.Context, d Deps, o Options) (Gathered, error) {
	if err := ctx.Err(); err != nil {
		return Gathered{}, err
	}
	g := withLead(d, o)
	return gatherParts(ctx, d, g, o)
}

// withLead is a gathering with only its root and its lead, resolved once.
func withLead(d Deps, o Options) Gathered {
	// Root is the lead home's project root; in a submodule it differs from
	// the work project root that the Records port reads from.
	g := Gathered{Root: filepath.Dir(d.Registry.Dir)}
	if root, err := filepath.Abs(g.Root); err == nil {
		g.Root = root
	}
	if lead, err := d.Registry.ResolveLead(o.LeadID); err != nil {
		g.LeadErr = err
	} else {
		g.Lead, g.Address = &lead, session.Address(lead)
	}
	return g
}

// gatherParts reads the peers, records, asks and load of the lead that g
// resolved, or with none o.LeadID's.
func gatherParts(ctx context.Context, d Deps, g Gathered, o Options) (Gathered, error) {
	leadID := o.LeadID
	if g.Lead != nil {
		leadID = g.Lead.ID
	}
	gatherPeers(ctx, d, &g, leadID, o.Fast)
	gatherRecords(d, &g)
	g.Asks = openAsks(d.Registry.Dir, g.Address)
	if err := ctx.Err(); err != nil {
		return Gathered{}, err
	}
	return g, nil
}

// gatherPeers lists the peers, and counts the load against them when they
// were listed.
func gatherPeers(ctx context.Context, d Deps, g *Gathered, leadID string, fast bool) {
	peers, err := d.Peers(ctx, leadID, fast)
	switch {
	case errors.Is(err, peer.ErrNoRex):
		return
	case err != nil:
		g.Rex, g.PeersErr = true, err
		return
	}
	g.Rex, g.Peers = true, peers
	if d.Load != nil {
		g.Load = d.Load(ctx, leadID, peers)
	}
}

// gatherRecords reads the lead's records, or every record without a lead.
func gatherRecords(d Deps, g *Gathered) {
	id := ""
	if g.Lead != nil {
		id = g.Lead.ID
	}
	g.Records, g.RecordsErr = d.Records(id, g.Address)
}

// openAsks are the open asks to address; none without one, or when the bus
// cannot be read.
func openAsks(dir, address string) []Ask {
	if address == "" {
		return nil
	}
	asks, err := gate.OpenAsks(dir)
	if err != nil {
		return nil
	}
	var out []Ask
	for _, m := range asks {
		if m.To == address {
			out = append(out, Ask{N: m.N, From: m.From, Summary: m.S, PersonOnly: m.P})
		}
	}
	return out
}

// ShownRuns is the running records, then the newest Finished finished ones,
// in the order given (newest first). A lost record counts as finished.
func ShownRuns(records []delegate.LocatedRecord) []delegate.LocatedRecord {
	var running, finished []delegate.LocatedRecord
	for _, r := range records {
		if r.State == "running" {
			running = append(running, r)
		} else if len(finished) < Finished {
			finished = append(finished, r)
		}
	}
	return append(running, finished...)
}
