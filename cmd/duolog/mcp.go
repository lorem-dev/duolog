package main

import (
	"context"
	"os"

	"github.com/lorem-dev/duolog/internal/adapter/mcp"
	"github.com/spf13/cobra"
)

// newMCPCommand serves the duolog MCP server on stdin and stdout. Claude Code
// and Codex start it in every session (user scope); it lists tools only for
// a lead in a duolog project.
func newMCPCommand(d *cmdDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve the duolog MCP tools on stdio for a Claude or Codex lead",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			// Checked once per process start: every tools/list and tools/call
			// of this session sees the same answer.
			enabled, off := mcp.Enabled(os.Environ(), cwd)
			srv := &mcp.Server{App: mcpApp{d}, Enabled: enabled, Off: off, Version: version}
			return srv.Serve(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
}

type mcpApp struct{ d *cmdDeps }

// hintsFrom returns the lead hints of the MCP tool call whose context is
// ctx, which the tools pass to ResolveLead; a CLI command's context has none.
func hintsFrom(ctx context.Context) []string { return mcp.LeadHints(ctx) }

func (a mcpApp) Delegate(ctx context.Context, in mcp.DelegateInput) (mcp.DelegateOutput, error) {
	return a.d.mcpDelegate(ctx, in)
}

func (a mcpApp) Peer(ctx context.Context, in mcp.PeerInput) (mcp.PeerOutput, error) {
	return a.d.mcpPeer(ctx, in)
}

func (a mcpApp) AnswerAsk(ctx context.Context, in mcp.AnswerAskInput) (mcp.AnswerAskOutput, error) {
	return a.d.mcpAnswerAsk(ctx, in)
}

func (a mcpApp) Status(ctx context.Context) (mcp.StatusOutput, error) {
	return a.d.mcpStatus(ctx)
}
