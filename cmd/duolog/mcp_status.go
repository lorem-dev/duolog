package main

import (
	"context"
	"os"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/mcp"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/runsnap"
	"github.com/lorem-dev/duolog/internal/project"
)

// statusDelegations caps the delegations the status tool lists, newest first.
const statusDelegations = 20

// mcpStatus is the status tool: the lead's peers, open asks, delegations,
// load line and quota. It only reads: it moves no bus cursor, answers no ask
// and starts no run. A part it cannot read is left empty; only a missing
// project or lead fails it.
func (d *cmdDeps) mcpStatus(ctx context.Context) (mcp.StatusOutput, error) {
	out := statusFrom(runsnap.Gathered{})
	root, err := projectRoot()
	if err != nil {
		return out, err
	}
	reg, err := session.Open(project.LeadHome(root))
	if err != nil {
		return out, err
	}
	// The call's hints choose the lead first, among the threads of a Codex
	// app-server; the gathering then reads that lead by its ID.
	lead, err := reg.ResolveLead("", hintsFrom(ctx)...)
	if err != nil {
		return out, err
	}
	g, err := runsnap.Gather(ctx, d.snapDeps(reg), runsnap.Options{Now: time.Now(), LeadID: lead.ID})
	if err != nil {
		return out, err
	}
	if g.LeadErr != nil {
		return out, g.LeadErr
	}
	out = statusFrom(g)
	// The quota is the status tool's own: a gathering never reads it.
	if results, read, err := collectQuota(ctx, d, root); err == nil && read > 0 {
		out.Quota = quotaShort(results)
	}
	return out, nil
}

// statusFrom is the status tool's view of a gathering: its peers, its open
// asks, its newest delegations up to statusDelegations, each with the
// checkout it ran in and its record, and the load line. A peer or a worker
// has no load line, as in the lead hooks.
func statusFrom(g runsnap.Gathered) mcp.StatusOutput {
	out := mcp.StatusOutput{Peers: []mcp.PeerRow{}, OpenAsks: []mcp.Ask{}, Delegations: []mcp.Delegation{}}
	for _, p := range g.Peers {
		out.Peers = append(out.Peers, mcp.PeerRow{ID: p.ID, Role: p.Role, Agent: p.Agent, State: p.Display(), Task: p.Task, WorkRoot: p.WorkRoot})
	}
	for _, a := range g.Asks {
		out.OpenAsks = append(out.OpenAsks, mcp.Ask{N: a.N, From: a.From, Summary: a.Summary, PersonOnly: a.PersonOnly})
	}
	for _, r := range g.Records {
		if len(out.Delegations) == statusDelegations {
			break
		}
		out.Delegations = append(out.Delegations, mcp.Delegation{TaskPath: r.TaskPath, Role: r.Role, State: r.State,
			AnswerPath: r.Path, WorkRoot: r.WorkRoot})
	}
	if os.Getenv("DUOLOG_PEER") == "" && !isWorker() {
		out.Load = g.Load.Line()
	}
	return out
}
