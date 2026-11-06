// Package remember writes the approval of `duolog remember --native` into the
// files that the agents themselves read, so the lead is not asked again.
package remember

import (
	"fmt"
	"io"
)

// NativeAllow writes one agent's own allow rule. Each method returns what to
// print after "<agent>: ": the rule it wrote, the rule that was there already,
// or why there is no rule.
type NativeAllow interface {
	// Allow allows the command that match names: a glob that ends in " *" is a
	// prefix, any other match is one exact command.
	Allow(root, match string) (string, error)
	// AllowMCP allows one tool of an MCP server, or a glob of tools.
	AllowMCP(root, server, tool string) (string, error)
}

// Agent is an agent that --native writes for. A nil Allow means the agent has
// no files for it.
type Agent struct {
	Name  string
	Allow NativeAllow
}

// Allow writes the rule for the command to each agent in order and prints one
// line for each.
func Allow(out io.Writer, root, match string, agents []Agent) error {
	return each(out, agents, func(a NativeAllow) (string, error) { return a.Allow(root, match) })
}

// AllowMCP writes the rule for the MCP tool to each agent in order and prints
// one line for each.
func AllowMCP(out io.Writer, root, server, tool string, agents []Agent) error {
	return each(out, agents, func(a NativeAllow) (string, error) { return a.AllowMCP(root, server, tool) })
}

func each(out io.Writer, agents []Agent, write func(NativeAllow) (string, error)) error {
	for _, agent := range agents {
		if agent.Allow == nil {
			fmt.Fprintf(out, "%s: no rule\n", agent.Name)
			continue
		}
		line, err := write(agent.Allow)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s: %s\n", agent.Name, line)
	}
	return nil
}
