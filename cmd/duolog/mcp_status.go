package main

import (
	"context"
	"encoding/json"
	"os"
	"runtime"

	"github.com/lorem-dev/duolog/internal/adapter/mcp"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/gate"
	"github.com/lorem-dev/duolog/internal/app/hook"
	"github.com/lorem-dev/duolog/internal/app/peer"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/spf13/cobra"
)

// statusDelegations caps the delegations the status tool lists, newest first.
const statusDelegations = 20

// mcpStatus is the status tool: the lead's peers, open asks, delegations,
// load line and quota. It only reads: it moves no bus cursor, answers no ask
// and starts no run. A part it cannot read is left empty; only a missing
// project or lead fails it.
func (d *cmdDeps) mcpStatus(ctx context.Context) (mcp.StatusOutput, error) {
	out := mcp.StatusOutput{Peers: []mcp.PeerRow{}, OpenAsks: []mcp.Ask{}, Delegations: []mcp.Delegation{}}
	root, err := projectRoot()
	if err != nil {
		return out, err
	}
	leadDir := project.LeadHome(root)
	reg, err := session.Open(leadDir)
	if err != nil {
		return out, err
	}
	lead, err := reg.ResolveLead("")
	if err != nil {
		return out, err
	}
	address := session.Address(lead)
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)

	out.Peers = statusPeers(d, cmd, lead.ID)
	out.OpenAsks = statusAsks(leadDir, address)
	out.Delegations = statusDelegationRows(reg, root, lead.ID, address)

	input, _ := json.Marshal(map[string]string{"session_id": lead.ID})
	env := hook.Env{Root: root, Agent: lead.Agent, Input: input, Environ: os.Environ(), Load: leadLoad(d, cmd)}
	if line, err := hook.LoadLine(ctx, env); err == nil {
		out.Load = line
	}
	if results, read, err := collectQuota(ctx, d, root); err == nil && read > 0 {
		out.Quota = quotaShort(results)
	}
	return out, nil
}

// statusPeers lists the lead's peers. They need Rex and do not exist on
// Windows; a lead without them has none to list.
func statusPeers(d *cmdDeps, cmd *cobra.Command, leadID string) []mcp.PeerRow {
	rows := []mcp.PeerRow{}
	if runtime.GOOS == "windows" {
		return rows
	}
	pctx, cancel, err := peerContext(d, cmd, leadID, false, 0)
	if err != nil {
		return rows
	}
	defer cancel()
	listed, err := peer.List(pctx)
	if err != nil {
		return rows
	}
	for _, p := range listed {
		rows = append(rows, mcp.PeerRow{ID: p.ID, Role: p.Role, Agent: p.Agent, State: p.Display(), Task: p.Task, WorkRoot: p.WorkRoot})
	}
	return rows
}

// statusAsks lists the open asks addressed to the lead.
func statusAsks(stateDir, address string) []mcp.Ask {
	rows := []mcp.Ask{}
	asks, err := gate.OpenAsks(stateDir)
	if err != nil {
		return rows
	}
	for _, m := range asks {
		if m.To == address {
			rows = append(rows, mcp.Ask{N: m.N, From: m.From, Summary: m.S, PersonOnly: m.P})
		}
	}
	return rows
}

// statusDelegationRows lists the lead's newest delegations across the work
// projects of its lead home, each with the checkout it ran in and its record
// in that project; a running one whose process is gone reads as lost.
func statusDelegationRows(reg *session.Registry, root, leadID, address string) []mcp.Delegation {
	rows := []mcp.Delegation{}
	records, err := delegate.LeadRecords(reg, root, "")
	if err != nil {
		return rows
	}
	for _, r := range records {
		if len(rows) == statusDelegations {
			break
		}
		if r.Lead == leadID || r.Lead == address {
			rows = append(rows, mcp.Delegation{TaskPath: r.TaskPath, Role: r.Role, State: r.State,
				AnswerPath: r.Path, WorkRoot: r.WorkRoot})
		}
	}
	return rows
}
