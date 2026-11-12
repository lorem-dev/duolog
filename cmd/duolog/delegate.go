package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/learn"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/wiring"
	"github.com/spf13/cobra"
)

// delegateFlags are the flags of duolog delegate.
type delegateFlags struct {
	taskPath, thread, lead, features, class, use, escalateFrom, reason, resultFile string
	needs                                                                          []string
	allowManual, skipPreflight, force                                              bool
}

// delegateFunc runs one delegation, as cmdDeps.runDelegate does.
type delegateFunc func(context.Context, delegate.Request) (delegate.Outcome, error)

func newDelegateCommand(d *cmdDeps) *cobra.Command {
	var f delegateFlags
	cmd := &cobra.Command{Use: "delegate <role>", Short: "Delegate a task and print its answer", Long: "Delegate a task and print its answer.\n\nThe task file needs the sections ## Goal, ## Read, ## Write, ## Limits and ## Check.", Args: cobra.ExactArgs(1), SilenceErrors: true, RunE: func(cmd *cobra.Command, args []string) error {
		if f.resultFile == "" {
			return f.run(cmd, args[0], d.runDelegateRecorded(cmd.ErrOrStderr(), args[0], f.taskPath, f.lead))
		}
		// The MCP server wrote and indexed the record; the run adds the task folder.
		run := withBegun(d.runDelegateRecording(f.resultFile), recordBegun(f.resultFile, cmd.ErrOrStderr(), false))
		return runRecorded(cmd.ErrOrStderr(), f.resultFile, args[0], f.taskPath, d.Notify, run, func(run delegateFunc) error {
			return f.run(cmd, args[0], run)
		})
	}}
	cmd.Flags().StringSliceVar(&f.needs, "needs", nil, "Capabilities the task relies on, as kind:name with kind skill, plugin or mcp")
	cmd.Flags().StringVar(&f.taskPath, "task", "", "Task file to delegate")
	cmd.Flags().StringVar(&f.thread, "thread", "", "Conversation thread to reuse")
	cmd.Flags().StringVar(&f.lead, "lead", "", "Registered lead session ID (defaults to DUOLOG_LEAD)")
	cmd.Flags().StringVar(&f.features, "features", "", "Task features for learning mode, as name=value pairs separated by commas")
	cmd.Flags().StringVar(&f.class, "class", "", "Task class for learning mode, instead of the one the features give")
	cmd.Flags().StringVar(&f.use, "use", "", "Run on this target, as agent/model/effort, instead of the routed one; it does not bypass the quota reserve")
	cmd.Flags().StringVar(&f.escalateFrom, "escalate-from", "", "In learning mode, the delegation id to leave for the next entry of its class ladder")
	cmd.Flags().StringVar(&f.reason, "reason", "", "Why the delegation is left: "+strings.Join(learn.EscalationReasons, ", "))
	cmd.Flags().BoolVar(&f.allowManual, "allow-manual-model", false, "Allow models reserved for manual selection")
	cmd.Flags().BoolVar(&f.skipPreflight, "skip-preflight", false, "Start the worker without judging the task's Check commands with the gate first")
	cmd.Flags().BoolVar(&f.force, "force", false, "Start a worker even for a task that routing.small_task_files keeps in the lead")
	cmd.Flags().StringVar(&f.resultFile, "result-file", "", "Delegation record to write the result to, for duolog mcp")
	_ = cmd.Flags().MarkHidden("result-file")
	_ = cmd.MarkFlagRequired("task")
	return cmd
}

// run is duolog delegate <role> with these flags, the delegation run by
// runDelegate.
func (f *delegateFlags) run(cmd *cobra.Command, role string, runDelegate delegateFunc) error {
	opts := delegate.Options{Lead: f.lead, AllowManualModel: f.allowManual, SkipPreflight: f.skipPreflight, Force: f.force, Needs: f.needs, Class: f.class, EscalateFrom: f.escalateFrom, EscalateReason: f.reason, Stderr: cmd.ErrOrStderr(),
		// A run with a result file is the MCP server's detached child.
		Detached: f.resultFile != ""}
	parsed, err := learn.ParseFeatures(f.features)
	if err == nil && (f.escalateFrom == "") != (f.reason == "") {
		err = errors.New("--escalate-from and --reason go together: name the delegation you leave and why")
	}
	if err == nil && f.use != "" {
		var entry config.LadderEntry
		if entry, err = config.ParseEntryKey(f.use); err == nil {
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
	cfg, err := configfile.Load(root, wiring.ConfigOptions())
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
	out, err := runDelegate(cmd.Context(), delegate.Request{Config: cfg, StateDir: s.Dir, Role: role, TaskPath: f.taskPath, Thread: f.thread, Options: opts})
	if out.Line != "" {
		if _, writeErr := fmt.Fprintln(cmd.OutOrStdout(), out.Line); writeErr != nil {
			return writeErr
		}
	}
	if err == nil && len(out.Needs) > 0 {
		return &needsExit{}
	}
	return err
}

// runRecorded runs body, the command with --result-file, and then writes the
// MCP server's record at path: the delegation's outcome when body ran it, else
// the refusal that came before it. It returns body's error, so the output and
// exit code stay the CLI's. notify alerts the person of a run that ended
// needing them; nil alerts none.
func runRecorded(stderr io.Writer, path, role, taskPath string, notify func(title, message string), runDelegate delegateFunc, body func(delegateFunc) error) error {
	markStarted(stderr, path)
	var out delegate.Outcome
	var runErr error
	ran := false
	err := body(func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		out, runErr = runDelegate(ctx, r)
		ran = true
		return out, runErr
	})
	recorded := err
	if ran {
		recorded = runErr
	}
	writeDelegateResult(stderr, path, role, taskPath, out, recorded, notify)
	return err
}

// markStarted puts the PID of this process, the detached run, in the running
// record at path, so a reader tells a run that died from one still going.
// The run writes it and not the server that started it: a write by the
// server could land after the run's final record and hide its answer. A
// record that is missing or no longer running stays as it is.
func markStarted(stderr io.Writer, path string) {
	r, err := delegate.ReadRecord(path, nil)
	if err != nil || r.State != "running" {
		return
	}
	r.PID = os.Getpid()
	if err := delegate.WriteRecord(path, r); err != nil {
		_, _ = fmt.Fprintf(stderr, "duolog delegate: cannot write the PID to the record %s: %v\n", path, err)
	}
}

// writeDelegateResult ends a detached run: it writes the record for out and
// err over the running record at path, keeping that record's fields. A record
// it cannot write is told on stderr, the run's log; the exit code stays the
// CLI's. Then it alerts the person through notify, when it is not nil, of a
// run that ended needing them (runEndNotice).
func writeDelegateResult(stderr io.Writer, path, role, taskPath string, out delegate.Outcome, err error, notify func(title, message string)) {
	base, readErr := delegate.ReadRecord(path, nil)
	if readErr != nil {
		base = delegate.Record{ID: strings.TrimSuffix(filepath.Base(path), ".json"), Role: role, TaskPath: taskPath}
	}
	r := delegate.RecordFor(base, out, err, time.Now().UTC())
	if writeErr := delegate.WriteRecord(path, r); writeErr != nil {
		_, _ = fmt.Fprintf(stderr, "duolog delegate: cannot write the result record %s: %v\n", path, writeErr)
	}
	runEndNotice(r, notify)
}

// runEndNotice shows the person a desktop notice for a detached run that ended
// failed, needing input or handed back to the lead: the lead may have ended
// its turn while the run went on. A done run is told by the lead's notice and
// its Stop alert. lead.notify false, a config that cannot be read and a panic
// give none; nothing here changes the run's record or exit.
func runEndNotice(r delegate.Record, notify func(title, message string)) {
	if notify == nil {
		return
	}
	words := map[string]string{"failed": "failed", "needs": "needs input", "run_here": "run here"}
	word, ok := words[r.State]
	if !ok {
		return
	}
	defer func() { _ = recover() }()
	root := r.WorkRoot
	if root == "" {
		var err error
		if root, err = projectRoot(); err != nil {
			return
		}
	}
	cfg, err := configfile.Load(root, wiring.ConfigOptions())
	if err != nil || !cfg.Lead.Notifies() {
		return
	}
	name := "run " + r.ID
	if r.Role != "" {
		name += " (" + r.Role + ")"
	}
	// Synchronous: the detached run exits next, and would lose a notice still
	// shown from a goroutine.
	notify("duolog", name+" "+word)
}
