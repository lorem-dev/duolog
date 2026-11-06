// Package usage estimates costs from delegate's usage.ndjson records.
package usage

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/lorem-dev/duolog/internal/config"
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

// Summarize loads prices for the project containing .duolog/usage.ndjson.
// A record without cache writes, as every record before they were kept, is
// priced as before.
func Summarize(path string) (Table, error) {
	var table Table
	cfg, err := config.Load(filepath.Dir(filepath.Dir(path)))
	if err != nil {
		return table, err
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return table, nil
	}
	if err != nil {
		return table, err
	}
	defer f.Close()
	rows := make(map[string]Row)
	warnings := make(map[string]bool)
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 4096), 1024*1024)
	line := 0
	for scan.Scan() {
		line++
		var record Row
		if err := json.Unmarshal(scan.Bytes(), &record); err != nil {
			return Table{}, fmt.Errorf("usage line %d: %w", line, err)
		}
		if !record.Valid() {
			return Table{}, fmt.Errorf("usage line %d: invalid token counts", line)
		}
		alias := route.Normalize(record.Agent, record.Model)
		price := cfg.Models[alias]
		if cost, priced := Cost(cfg, record.Agent, record.Model, record.Tokens); !priced {
			warnings[fmt.Sprintf("No price for %s model %s; cost excluded", record.Agent, record.Model)] = true
		} else {
			record.Cost = cost
			if alias == record.Model {
				warnings[fmt.Sprintf("Estimated %s using prices for %s; actual version unknown (usage records the routed alias)", alias, price.PricedFor)] = true
			} else if price.PricedFor != record.Model {
				warnings[fmt.Sprintf("Estimated %s using prices for %s; version that ran was %s", alias, price.PricedFor, record.Model)] = true
			}
			if price.Note != "" {
				warnings[alias+": "+price.Note] = true
			}
		}
		key := record.Agent + "\x00" + record.Model + "\x00" + record.Role
		row := rows[key]
		row.Role, row.Agent, row.Model = record.Role, record.Agent, record.Model
		row.Add(record.Tokens)
		row.Cost += record.Cost
		rows[key] = row
		table.Total += record.Cost
	}
	if err := scan.Err(); err != nil {
		return Table{}, err
	}
	keys := make([]string, 0, len(rows))
	for key := range rows {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		table.Rows = append(table.Rows, rows[key])
	}
	for warning := range warnings {
		table.Warnings = append(table.Warnings, warning)
	}
	sort.Strings(table.Warnings)
	return table, nil
}
