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
	"github.com/lorem-dev/duolog/internal/models"
)

type Row struct {
	Role   string  `json:"role"`
	Agent  string  `json:"agent"`
	Model  string  `json:"model"`
	Input  int64   `json:"input"`
	Cached int64   `json:"cached"`
	Output int64   `json:"output"`
	Cost   float64 `json:"-"`
}
type Table struct {
	Rows     []Row
	Total    float64
	Warnings []string
}

// Summarize loads prices for the project containing .duolog/usage.ndjson.
// Input includes cached reads, so the cached subset is charged only once.
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
		if record.Input < 0 || record.Cached < 0 || record.Output < 0 || record.Cached > record.Input {
			return Table{}, fmt.Errorf("usage line %d: invalid token counts", line)
		}
		alias := models.Normalize(record.Agent, record.Model)
		price, ok := cfg.Models[alias]
		if !ok || price.Agent != record.Agent {
			warnings[fmt.Sprintf("No price for %s model %s; cost excluded", record.Agent, record.Model)] = true
		} else {
			record.Cost = (float64(record.Input-record.Cached)*price.Input + float64(record.Cached)*price.Cached + float64(record.Output)*price.Output) / 1e6
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
		row.Input += record.Input
		row.Cached += record.Cached
		row.Output += record.Output
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
