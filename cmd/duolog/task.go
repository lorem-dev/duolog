package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/learning"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/spf13/cobra"
)

func newTaskCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "task", Short: "Record learning-mode tasks"}
	cmd.AddCommand(newTaskStartCommand(), newTaskFinishCommand(), newTaskOutcomeCommand())
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
			reason := "not a Git repository"
			if journal.InGit(workRoot(root)) {
				reason = "no commits yet"
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "task %s started; %s, so it cannot be profiled\n", task.ID, reason)
		} else {
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "task %s started at %s\n", task.ID, base[:7])
		}
		return err
	}}
	cmd.Flags().StringVar(&lead, "lead", "", "Registered lead session ID (defaults to DUOLOG_LEAD)")
	return cmd
}

func newTaskFinishCommand() *cobra.Command {
	var lead string
	cmd := &cobra.Command{Use: "finish", Short: "Close the open task and report its delegations", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
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
		j, err := journal.Open(s.Dir)
		if err != nil {
			return err
		}
		task, ok, err := j.CurrentTask(l.ID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("no open task for this lead")
		}
		ds, ts, vs, err := readLearningJournal(j)
		if err != nil {
			return err
		}
		var runs []journal.Delegation
		for _, d := range ds {
			if d.Task == task.ID && d.ProfileOf == "" {
				runs = append(runs, d)
			}
		}
		slices.SortStableFunc(runs, func(a, b journal.Delegation) int {
			if a.Step < b.Step {
				return -1
			}
			if a.Step > b.Step {
				return 1
			}
			return 0
		})
		final, err := journal.Head(workRoot(root))
		if err != nil {
			return err
		}
		tree, err := journal.Capture(workRoot(root))
		if err != nil {
			return err
		}
		record := journal.Task{Kind: "finish", Time: time.Now().UTC(), ID: task.ID, Lead: task.Lead, Title: task.Title, Base: task.Base, Final: final, FinalTree: tree}
		for _, d := range runs {
			record.Delegations = append(record.Delegations, d.ID)
			record.USD += d.USD
		}
		st := learning.Aggregate(cfg, ds, ts, vs)
		offers := learning.Offers(cfg, runs, st, vs)
		changed := learning.Changed(cfg, learning.Propose(cfg, st, vs))
		if err := j.Append("tasks.ndjson", record); err != nil {
			return err
		}
		if _, err := j.CloseTask(l.ID); err != nil {
			return err
		}
		return learning.WriteReport(cmd.OutOrStdout(), cfg, task, runs, offers, changed, false)
	}}
	cmd.Flags().StringVar(&lead, "lead", "", "Registered lead session ID (defaults to DUOLOG_LEAD)")
	return cmd
}

func newTaskOutcomeCommand() *cobra.Command {
	var note string
	cmd := &cobra.Command{Use: "outcome <task id> <accepted|small_fixes|reworked|dropped>", Short: "Record the person's outcome for a finished task", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		if isWorker() {
			return fmt.Errorf("workers cannot change the journal")
		}
		if _, ok := journal.Outcomes[args[1]]; !ok {
			return fmt.Errorf("outcome must be one of accepted, small_fixes, reworked, dropped")
		}
		root, err := projectRoot()
		if err != nil {
			return err
		}
		j := &journal.Journal{Dir: filepath.Join(root, ".duolog", "journal")}
		ts, err := journal.ReadAll[journal.Task](j, "tasks.ndjson")
		if err != nil {
			return err
		}
		for _, t := range ts {
			if t.ID == args[0] && t.Kind == "finish" {
				return j.Append("tasks.ndjson", journal.Task{Kind: "outcome", Time: time.Now().UTC(), ID: t.ID, Outcome: args[1], Note: note})
			}
		}
		return fmt.Errorf("task %s is not finished", args[0])
	}}
	cmd.Flags().StringVar(&note, "note", "", "One-line note about the outcome")
	return cmd
}

func readLearningJournal(j *journal.Journal) (ds []journal.Delegation, ts []journal.Task, vs []journal.Verdict, err error) {
	ds, err = journal.ReadAll[journal.Delegation](j, "delegations.ndjson")
	if err != nil {
		return
	}
	ts, err = journal.ReadAll[journal.Task](j, "tasks.ndjson")
	if err != nil {
		return
	}
	vs, err = journal.ReadAll[journal.Verdict](j, "verdicts.ndjson")
	return
}
