package telemetry

import (
	"cmp"
	"math"
	"slices"
	"time"
)

// Daily is one row of the report: the run attempts of one UTC day, agent,
// role and outcome. In, Cached and Out add up the runs whose usage is known,
// a known zero included; UsageUnknown counts the others, which add no tokens.
// USDmicro adds up priced runs only; Unpriced counts the others, which add no
// cost, so no cost is ever estimated. The fields are in table order.
type Daily struct {
	Day          string `json:"day"`
	Agent        string `json:"agent"`
	Role         string `json:"role"`
	Outcome      string `json:"outcome"`
	Runs         int64  `json:"runs"`
	In           int64  `json:"in"`
	Cached       int64  `json:"cached"`
	Out          int64  `json:"out"`
	USDmicro     int64  `json:"usd_micro"`
	Secs         int64  `json:"secs"`
	Asks         int64  `json:"asks"`
	Denials      int64  `json:"denials"`
	Unpriced     int64  `json:"unpriced"`
	UsageUnknown int64  `json:"usage_unknown"`
}

// QuotaPoint is one quota snapshot on the curve.
type QuotaPoint struct {
	T     int64 `json:"t"`
	Quota Quota `json:"quota"`
}

// Move is a routing decision whose chosen target differs from the incumbent.
type Move struct {
	T     int64 `json:"t"`
	Route Route `json:"route"`
}

// Report aggregates what Read returns for the person's terminal: runs per
// day, agent, role and outcome, sorted by those; quota snapshots and balance
// moves ordered by time. Its slices are never nil, so JSON shows them empty.
type Report struct {
	Daily []Daily      `json:"daily"`
	Quota []QuotaPoint `json:"quota"`
	Moves []Move       `json:"moves"`
}

// Report reads the events q selects and aggregates them. It reads telemetry
// only, never the .duolog logs, and writes nothing.
func (s *Store) Report(q Query) (Report, error) {
	events, err := s.Read(q)
	if err != nil {
		return Report{}, err
	}
	r := Report{Daily: []Daily{}, Quota: []QuotaPoint{}, Moves: []Move{}}
	type key struct{ day, agent, role, outcome string }
	rows := map[key]*Daily{}
	for _, e := range events {
		switch e := e.(type) {
		case Run:
			k := key{time.Unix(e.T, 0).UTC().Format(time.DateOnly), e.Agent, e.Role, e.Outcome}
			d := rows[k]
			if d == nil {
				d = &Daily{Day: k.day, Agent: k.agent, Role: k.role, Outcome: k.outcome}
				rows[k] = d
			}
			d.add(e)
		case Quota:
			r.Quota = append(r.Quota, QuotaPoint{T: e.T, Quota: e})
		case Route:
			if e.Chosen != e.Incumbent {
				r.Moves = append(r.Moves, Move{T: e.T, Route: e})
			}
		}
	}
	for _, d := range rows {
		r.Daily = append(r.Daily, *d)
	}
	slices.SortFunc(r.Daily, func(a, b Daily) int {
		return cmp.Or(cmp.Compare(a.Day, b.Day), cmp.Compare(a.Agent, b.Agent),
			cmp.Compare(a.Role, b.Role), cmp.Compare(a.Outcome, b.Outcome))
	})
	slices.SortStableFunc(r.Quota, func(a, b QuotaPoint) int { return cmp.Compare(a.T, b.T) })
	slices.SortStableFunc(r.Moves, func(a, b Move) int { return cmp.Compare(a.T, b.T) })
	return r, nil
}

// add counts one run attempt into the row.
func (d *Daily) add(e Run) {
	d.Runs = plus(d.Runs, 1)
	if e.UsageKnown {
		d.In = plus(d.In, e.In)
		d.Cached = plus(d.Cached, e.Cached)
		d.Out = plus(d.Out, e.Out)
	} else {
		d.UsageUnknown = plus(d.UsageUnknown, 1)
	}
	if e.Priced {
		d.USDmicro = plus(d.USDmicro, e.USDmicro)
	} else {
		d.Unpriced = plus(d.Unpriced, 1)
	}
	d.Secs = plus(d.Secs, e.Secs)
	d.Asks = plus(d.Asks, e.Asks)
	d.Denials = plus(d.Denials, e.Denials)
}

// plus adds two counts Read keeps non-negative, stopping at the int64 limit
// instead of wrapping.
func plus(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}
