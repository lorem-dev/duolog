package journal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/usage"
)

// SummarizeUsage loads prices for the project containing .duolog/usage.ndjson.
// A record without cache writes, as every record before they were kept, is
// priced as before.
func SummarizeUsage(path string) (usage.Table, error) {
	var table usage.Table
	cfg, err := configfile.Load(filepath.Dir(filepath.Dir(path)))
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
	rows := make(map[string]usage.Row)
	warnings := make(map[string]bool)
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 4096), 1024*1024)
	line := 0
	for scan.Scan() {
		line++
		var record usage.Row
		if err := json.Unmarshal(scan.Bytes(), &record); err != nil {
			return usage.Table{}, fmt.Errorf("usage line %d: %w", line, err)
		}
		if !record.Valid() {
			return usage.Table{}, fmt.Errorf("usage line %d: invalid token counts", line)
		}
		alias := route.Normalize(record.Agent, record.Model)
		price := cfg.Models[alias]
		if cost, priced := usage.Cost(cfg, record.Agent, record.Model, record.Tokens); !priced {
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
		return usage.Table{}, err
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
