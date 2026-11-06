// Package usage estimates costs from delegate's usage.ndjson records.
package usage

import (
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/route"
)

// Tokens counts the tokens of one or more runs. Input is the total input:
// Cached (cache reads) and Write (cache writes) are disjoint subsets of it, and
// WriteHour is the subset of Write kept in the cache for an hour. Codex reports
// no cache writes; a record written before cache writes were kept has none.
type Tokens struct {
	Input     int64 `json:"input"`
	Cached    int64 `json:"cached"`
	Write     int64 `json:"cache_write,omitempty"`
	WriteHour int64 `json:"cache_write_1h,omitempty"`
	Output    int64 `json:"output"`
}

// Add adds o to t.
func (t *Tokens) Add(o Tokens) {
	t.Input += o.Input
	t.Cached += o.Cached
	t.Write += o.Write
	t.WriteHour += o.WriteHour
	t.Output += o.Output
}

// Valid reports whether every count is non-negative and the subsets fit.
func (t Tokens) Valid() bool {
	return t.Input >= 0 && t.Cached >= 0 && t.Write >= 0 && t.WriteHour >= 0 && t.Output >= 0 &&
		t.Cached <= t.Input && t.Write <= t.Input-t.Cached && t.WriteHour <= t.Write
}

// Claude charges a cache write as a multiple of the input price: 1.25 for the
// five-minute cache and 2 for the one-hour cache.
const (
	WriteRate     = 1.25
	WriteHourRate = 2.0
)

type Row struct {
	Role  string `json:"role"`
	Agent string `json:"agent"`
	Model string `json:"model"`
	Tokens
	Cost float64 `json:"-"`
}
type Table struct {
	Rows     []Row
	Total    float64
	Warnings []string
}

// Cost prices a run in USD; false means the model has no price for the agent.
// Input includes cache reads and writes, so each subset is charged only once:
// reads at the cached price, writes at a multiple of the input price.
func Cost(cfg *config.Config, agent, model string, t Tokens) (float64, bool) {
	price, ok := cfg.Models[route.Normalize(agent, model)]
	if !ok || price.Agent != agent {
		return 0, false
	}
	plain := t.Input - t.Cached - t.Write
	minute := t.Write - t.WriteHour
	return (float64(plain)*price.Input + float64(t.Cached)*price.Cached +
		float64(minute)*price.Input*WriteRate + float64(t.WriteHour)*price.Input*WriteHourRate +
		float64(t.Output)*price.Output) / 1e6, true
}
