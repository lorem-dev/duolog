package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/core/learn"
	"github.com/lorem-dev/duolog/internal/journal"
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
		if err != nil {
			return err
		}
		_, err = fmt.Fprint(cmd.OutOrStdout(), protocol(cfg))
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
		declined, err := declinedClasses(j)
		if err != nil {
			return err
		}
		classProposal := len(learn.ProposeClasses(cfg, ds, declined)) > 0
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
		st := learn.Aggregate(cfg, ds, ts, vs)
		offers := learn.Offers(cfg, runs, st, vs)
		changed := learn.Changed(cfg, learn.Propose(cfg, st, vs))
		if err := j.Append("tasks.ndjson", record); err != nil {
			return err
		}
		if _, err := j.CloseTask(l.ID); err != nil {
			return err
		}
		return learn.WriteReport(cmd.OutOrStdout(), cfg, task, runs, offers, changed, classProposal)
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

// protocol gives the lead the fixed learning-mode instructions and the
// project's class descriptions in name order.
func protocol(cfg *config.Config) string {
	const start = `Learning mode routes each delegation by its class. For every duolog delegate in this task:
- Add --features with what you know, output first:
  output=code|plan|review|tests|docs|diagnosis exemplar=yes|partly|no components=1|2+
  outside_evidence=yes|no scope=1-3|4-20|20+ ambiguity=low|high target=code|plan
  Join them with commas. Or name the class with --class <name>. With neither, the
  role's own target runs and the delegation counts as unclassified.
- If the person names a model or way to run a step, use it (--use); if unsure what
  they meant, ask.
- Skeleton work is three delegations with --class skeleton: plan, then review_final
  of the plan, then implement_complex.
- Escalate with --escalate-from <id> --reason <signal> when the worker gave no
  reproduction (no_repro) or no line references (no_lines), explained inconsistently
  (inconsistent), did not turn the test green (not_green) or ran out of turns
  (out_of_turns). The result line shows the id.
Classes:
`
	var out strings.Builder
	out.WriteString(start)
	var names []string
	for name := range cfg.Learning.Classes {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		c := cfg.Learning.Classes[name]
		fmt.Fprintf(&out, "%s (%s): %s\n", name, c.Priority, c.Description)
	}
	out.WriteString("When the task is done, run duolog task finish and show its output to the user as\nprinted, in that order.\n")
	return out.String()
}
