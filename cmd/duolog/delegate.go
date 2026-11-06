package main

import (
	"errors"
	"fmt"
	"strings"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/learn"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/spf13/cobra"
)

// runDelegate is delegate.Run; tests replace it to see the options.
var runDelegate = delegate.Run

func newDelegateCommand() *cobra.Command {
	var taskPath, thread, lead, features, class, use, escalateFrom, reason string
	var needs []string
	var allowManual bool
	cmd := &cobra.Command{Use: "delegate <role>", Short: "Delegate a task and print its answer", Long: "Delegate a task and print its answer.\n\nThe task file needs the sections ## Goal, ## Read, ## Write, ## Limits and ## Check.", Args: cobra.ExactArgs(1), SilenceErrors: true, RunE: func(cmd *cobra.Command, args []string) error {
		opts := delegate.Options{Lead: lead, AllowManualModel: allowManual, Needs: needs, Class: class, EscalateFrom: escalateFrom, EscalateReason: reason}
		parsed, err := learn.ParseFeatures(features)
		if err == nil && (escalateFrom == "") != (reason == "") {
			err = errors.New("--escalate-from and --reason go together: name the delegation you leave and why")
		}
		if err == nil && use != "" {
			var entry config.LadderEntry
			if entry, err = config.ParseEntryKey(use); err == nil {
				t := entry.Target()
				opts.Entry, opts.Person = &t, true
			}
		}
		if err != nil {
			_, writeErr := fmt.Fprintln(cmd.OutOrStdout(), err)
			return errors.Join(err, writeErr)
		}
		opts.Features = parsed
		opts.ProtectedRoots = launchProtectedRoots()
		root, err := projectRoot()
		if err != nil {
			return err
		}
		cfg, err := configfile.Load(root)
		if err != nil {
			return err
		}
		s, err := store.Open(root)
		if err != nil {
			return err
		}
		if tel := openTelemetry(cfg, root); tel != nil {
			defer tel.Close()
			opts.Observe = observeAttempt(tel, cfg)
		}
		ctx := delegate.WithOptions(cmd.Context(), opts)
		out, err := runDelegate(ctx, cfg, s, args[0], taskPath, thread)
		if out.Line != "" {
			if _, writeErr := fmt.Fprintln(cmd.OutOrStdout(), out.Line); writeErr != nil {
				return writeErr
			}
		}
		if err == nil && len(out.Needs) > 0 {
			return &needsExit{}
		}
		return err
	}}
	cmd.Flags().StringSliceVar(&needs, "needs", nil, "Capabilities the task relies on, as kind:name with kind skill, plugin or mcp")
	cmd.Flags().StringVar(&taskPath, "task", "", "Task file to delegate")
	cmd.Flags().StringVar(&thread, "thread", "", "Conversation thread to reuse")
	cmd.Flags().StringVar(&lead, "lead", "", "Registered lead session ID (defaults to DUOLOG_LEAD)")
	cmd.Flags().StringVar(&features, "features", "", "Task features for learning mode, as name=value pairs separated by commas")
	cmd.Flags().StringVar(&class, "class", "", "Task class for learning mode, instead of the one the features give")
	cmd.Flags().StringVar(&use, "use", "", "Run on this target, as agent/model/effort, instead of the routed one")
	cmd.Flags().StringVar(&escalateFrom, "escalate-from", "", "In learning mode, the delegation id to leave for the next entry of its class ladder")
	cmd.Flags().StringVar(&reason, "reason", "", "Why the delegation is left: "+strings.Join(learn.EscalationReasons, ", "))
	cmd.Flags().BoolVar(&allowManual, "allow-manual-model", false, "Allow models reserved for manual selection")
	_ = cmd.MarkFlagRequired("task")
	return cmd
}
