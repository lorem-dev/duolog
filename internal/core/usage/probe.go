package usage

// Probed is what a probe call reported: Tokens in the sense of Tokens (Cached
// and Write disjoint subsets of Input, WriteHour the part of Write kept for an
// hour), the CLI's own cost when it gave one, and whether the CLI reported
// usage at all.
type Probed struct {
	Tokens
	// CostUSD is the cost the CLI reported, nil when it gave none.
	CostUSD *float64
	// Known says that the CLI reported its usage; a probe that failed early
	// has none.
	Known bool
}
