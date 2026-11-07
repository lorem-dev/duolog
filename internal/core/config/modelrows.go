package config

import (
	"fmt"
	"maps"
	"slices"
)

// ModelMapAgents are the two agents that model_map names, in key and value
// order: model_map maps a claude alias to a codex alias.
var ModelMapAgents = [2]string{"claude", "codex"}

// Rows returns the rows model_map gives, one per entry in key order, followed
// by ModelRows.
func (a AgentSync) Rows() []map[string]string {
	rows := make([]map[string]string, 0, len(a.ModelMap)+len(a.ModelRows))
	for _, key := range slices.Sorted(maps.Keys(a.ModelMap)) {
		if a.ModelMap[key] == "" {
			continue
		}
		rows = append(rows, map[string]string{ModelMapAgents[0]: key, ModelMapAgents[1]: a.ModelMap[key]})
	}
	return append(rows, a.ModelRows...)
}

// CheckRows fails when a row of model_rows maps an alias of one agent to a
// different alias of another than model_map or an earlier row does. Two
// model_map entries that share a target are not checked here: the router
// refuses that reverse mapping when it needs it.
func (a AgentSync) CheckRows() error {
	seen := rowPairs{}
	for _, row := range a.Rows()[:len(a.Rows())-len(a.ModelRows)] {
		seen.add(row)
	}
	for _, row := range a.ModelRows {
		if err := seen.check(row); err != nil {
			return err
		}
		seen.add(row)
	}
	return nil
}

// rowPairs records, for each alias of an agent and each other agent in its
// row, the alias that the first row to say so gave.
type rowPairs map[[3]string]string

func (p rowPairs) add(row map[string]string) {
	for from, alias := range row {
		for to, target := range row {
			if _, ok := p[[3]string{from, alias, to}]; ok || from == to {
				continue
			}
			p[[3]string{from, alias, to}] = target
		}
	}
}

// check fails when the row maps an alias to another than a recorded one.
func (p rowPairs) check(row map[string]string) error {
	for _, from := range slices.Sorted(maps.Keys(row)) {
		for _, to := range slices.Sorted(maps.Keys(row)) {
			if other, ok := p[[3]string{from, row[from], to}]; ok && from != to && other != row[to] {
				return fmt.Errorf("sync.agents: %s %s maps to both %s and %s on %s; model_map and model_rows must agree", from, row[from], other, row[to], to)
			}
		}
	}
	return nil
}

// MapModel returns the alias of agent that stands for alias of from. It fails
// when no row maps it, or when more than one alias of agent does.
func (a AgentSync) MapModel(from, alias, agent string) (string, error) {
	match := ""
	for _, row := range a.Rows() {
		if row[from] != alias || row[agent] == "" {
			continue
		}
		if match != "" && match != row[agent] {
			return "", fmt.Errorf("ambiguous model mapping for %s", alias)
		}
		match = row[agent]
	}
	if match == "" {
		return "", fmt.Errorf("no model mapping for %s on %s", alias, agent)
	}
	return match, nil
}
