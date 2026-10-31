package main

import (
	"fmt"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/delegate"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/spf13/cobra"
)

func newDelegateCommand() *cobra.Command {
	var taskPath, thread, lead string
	var allowManual bool
	cmd := &cobra.Command{Use: "delegate <role>", Short: "Delegate a task and print its answer", Args: cobra.ExactArgs(1), SilenceErrors: true, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := projectRoot()
		if err != nil {
			return err
		}
		cfg, err := config.Load(root)
		if err != nil {
			return err
		}
		s, err := store.Open(root)
		if err != nil {
			return err
		}
		ctx := delegate.WithOptions(cmd.Context(), delegate.Options{Lead: lead, AllowManualModel: allowManual})
		out, err := delegate.Run(ctx, cfg, s, args[0], taskPath, thread)
		if out.Line != "" {
			if _, writeErr := fmt.Fprintln(cmd.OutOrStdout(), out.Line); writeErr != nil {
				return writeErr
			}
		}
		return err
	}}
	cmd.Flags().StringVar(&taskPath, "task", "", "Task file to delegate")
	cmd.Flags().StringVar(&thread, "thread", "", "Conversation thread to reuse")
	cmd.Flags().StringVar(&lead, "lead", "", "Registered lead session ID (defaults to DUOLOG_LEAD)")
	cmd.Flags().BoolVar(&allowManual, "allow-manual-model", false, "Allow models reserved for manual selection")
	_ = cmd.MarkFlagRequired("task")
	return cmd
}
