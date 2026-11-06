package record

import "time"

// The observations balance reports. Balance is not in the tree yet: until it
// lands, duolog quota reports the snapshots it reads and duolog route the
// routes it resolves through this seam, and balance takes the types over in
// its observe.go. Every string is a closed name and every number a count;
// telemetry writes anything else as other.
type WindowObservation struct {
	Kind, Family, Status           string
	LengthMinutes, UsedBasisPoints int64
	ResetAt                        time.Time
}

type QuotaObservation struct {
	Agent, Source, Plan   string
	Epoch                 int64
	Windows               []WindowObservation
	Overage, SpendControl bool
}

type RouteTarget struct{ Agent, Family, Effort string }

type RouteObservation struct {
	Role, Class       string
	Candidates        []RouteTarget
	Chosen, Incumbent RouteTarget
	Rule              string
	ScoreMin          float64
}

type ResetObservation struct{ Agent, Reason string }
