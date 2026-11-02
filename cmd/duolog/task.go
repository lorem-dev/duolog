package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/spf13/cobra"
)

func newTaskCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "task", Short: "Record learning-mode tasks"}
	cmd.AddCommand(newTaskStartCommand())
	return cmd
}

func newTaskStartCommand() *cobra.Command {
	var lead string
	cmd := &cobra.Command{Use: "start [title...]", Short: "Open a task and record its base commit", RunE: func(cmd *cobra.Command, args []string) error {
		if isWorker() {
			return fmt.Errorf("workers cannot change the journal")
		}
		root, err := projectRoot()
		if err != nil {
			return err
		}
		cfg, err := config.Load(root)
		if err != nil {
			return err
		}
		if !cfg.Learning.Enabled {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "learning mode is off in .duolog/config.yaml; nothing recorded")
			return err
		}
		s, err := store.Open(root)
		if err != nil {
			return err
		}
		reg, err := session.Open(s.Dir)
		if err != nil {
			return err
		}
		l, err := reg.ResolveLead(lead)
		if err != nil {
			return err
		}
		base, err := journal.Head(workRoot(root))
		if err != nil {
			return err
		}
		j, err := journal.Open(s.Dir)
		if err != nil {
			return err
		}
		task, err := j.StartTask(l.ID, strings.Join(args, " "), base, time.Now())
		if err != nil {
			return err
		}
		if base == "" {
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "task %s started; not a Git repository, so it cannot be profiled\n", task.ID)
		} else {
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "task %s started at %s\n", task.ID, base[:7])
		}
		return err
	}}
	cmd.Flags().StringVar(&lead, "lead", "", "Registered lead session ID (defaults to DUOLOG_LEAD)")
	return cmd
}
