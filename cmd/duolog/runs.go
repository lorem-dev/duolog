package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/peer"
	corerun "github.com/lorem-dev/duolog/internal/core/run"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/spf13/cobra"
)

const (
	// runsFinished is how many finished runs duolog runs lists after the
	// running ones.
	runsFinished = 5
	// watchPoll is the wait between reads of a run's log.
	watchPoll = 300 * time.Millisecond
)

// errWatchModel is the refusal when a model may read the output of watch.
var errWatchModel = errors.New("duolog watch prints a worker's stream only to your own terminal, so no model reads it; run it in a terminal outside the agent, or use duolog watch --tab when you ask for it")

// runsRegistry is the lead home registry of this project and the project's
// root, after the worker check: a worker neither lists nor follows delegate
// runs.
func runsRegistry() (reg *session.Registry, root string, err error) {
	if isWorker() {
		return nil, "", errors.New("workers cannot list or watch delegate runs")
	}
	root, err = projectRoot()
	if err != nil {
		return nil, "", err
	}
	reg, err = session.Open(project.LeadHome(root))
	if err != nil {
		return nil, "", err
	}
	return reg, root, nil
}

// runsLead is the id of the lead whose runs are listed: the explicit lead,
// DUOLOG_LEAD, or the lead that runs the command. It is "" when no lead
// resolves and none was asked for (no live lead, or several); then every
// record is listed. A lead that was asked for and does not resolve, or a
// registry that cannot be read, is an error: the list never widens.
func runsLead(reg *session.Registry, lead string) (string, error) {
	asked := lead != "" || session.CurrentLead(os.Environ()) != ""
	l, err := reg.ResolveLead(lead)
	if err == nil {
		return l.ID, nil
	}
	if asked {
		return "", fmt.Errorf("lead of the runs: %w", err)
	}
	if _, rerr := reg.Entries(); rerr != nil {
		return "", rerr
	}
	return "", nil
}

// listRuns returns the delegate records of this lead, from every work project
// that shares the lead home, newest first. lead "" lists all of them.
func listRuns(reg *session.Registry, root, lead string) ([]delegate.LocatedRecord, error) {
	return delegate.LeadRecords(reg, root, lead)
}

// shownRuns is the running records, then the newest finished ones. A record
// that is lost counts as finished.
func shownRuns(records []delegate.LocatedRecord) []delegate.LocatedRecord {
	var running, finished []delegate.LocatedRecord
	for _, r := range records {
		if r.State == "running" {
			running = append(running, r)
		} else if len(finished) < runsFinished {
			finished = append(finished, r)
		}
	}
	return append(running, finished...)
}

// newRunsCommand lists the project's workers: live peers, then delegate runs
// (running ones, then the last finished). It prints duolog's own data and
// never the worker's output, so a lead may run it.
func newRunsCommand(d *cmdDeps) *cobra.Command {
	var long, asJSON bool
	var lead string
	cmd := &cobra.Command{Use: "runs", Short: "List the workers of this project and of the submodules or superproject sharing its lead home", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if long && asJSON {
			return errors.New("--long and --json cannot be used together")
		}
		reg, root, err := runsRegistry()
		if err != nil {
			return err
		}
		rows, err := d.runRows(cmd, reg, root, lead)
		if err != nil {
			return err
		}
		return writeRunRows(cmd.OutOrStdout(), rows, long, asJSON, time.Now())
	}}
	cmd.Flags().BoolVar(&long, "long", false, "Print a table with kind, role, agent and model, age and worktree")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the rows as JSON: id, kind, role, agent, model, state, started, worktree, task_dir")
	cmd.Flags().StringVar(&lead, "lead", "", "Registered lead session ID for the peers and delegate runs (defaults to DUOLOG_LEAD)")
	return cmd
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// pickRun is the record named id, or the newest running one when id is empty.
func pickRun(records []delegate.LocatedRecord, id string) (delegate.LocatedRecord, error) {
	for _, r := range records {
		if (id == "" && r.State == "running") || (id != "" && r.ID == id) {
			return r, nil
		}
	}
	if id == "" {
		return delegate.LocatedRecord{}, errors.New("no delegate run is running; duolog runs lists them")
	}
	return delegate.LocatedRecord{}, fmt.Errorf("no delegate run %s; duolog runs lists them", id)
}

// newWatchCommand follows one delegate run for the person: the worker's
// messages and one line per tool call. Like peer show it is for the person's
// own terminal, so that no model reads a worker's stream; the lead hook also
// stops a lead's tool call that names it (peerShowLeadGuard). With --tab it
// opens a Rex tab that runs it, and prints one line.
func newWatchCommand(d *cmdDeps) *cobra.Command {
	var tab bool
	var client, lead string
	var lines int
	cmd := &cobra.Command{Use: "watch [id]", Short: "Follow a delegate run, or show a peer's screen, in your own terminal", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		// The refusal comes before anything is read.
		if !tab {
			if err := d.refuseWatch(cmd); err != nil {
				return err
			}
		}
		reg, root, err := runsRegistry()
		if err != nil {
			return err
		}
		id := ""
		scope := ""
		if len(args) == 1 {
			id = args[0]
		} else if scope, err = runsLead(reg, lead); err != nil {
			return err
		}
		records, err := listRuns(reg, root, scope)
		if err != nil {
			return err
		}
		rec, err := pickRun(records, id)
		if err != nil {
			// Not a delegate run: a live peer's id does what peer show and
			// peer view do.
			if id != "" && peerKnown(reg, id) {
				return d.watchPeer(cmd, id, tab, client, lead, lines)
			}
			return err
		}
		if tab {
			return d.watchTab(cmd, rec.ID, client)
		}
		return delegate.Watch(cmd.Context(), cmd.OutOrStdout(), rec.Path, session.ProcessAlive, watchPoll)
	}}
	cmd.Flags().BoolVar(&tab, "tab", false, "Open a read-only Rex tab that runs duolog watch and move your Rex client to it")
	cmd.Flags().StringVar(&client, "client", "", "With --tab, the Rex client to move (default: the one client that shows this session)")
	cmd.Flags().StringVar(&lead, "lead", "", "Registered lead session ID for a peer (defaults to DUOLOG_LEAD)")
	cmd.Flags().IntVar(&lines, "lines", peer.DefaultShowLines, fmt.Sprintf("For a peer, the number of lines, 1 to %d", peer.MaxShowLines))
	return cmd
}

// refuseWatch is the refusal when a model may read the output of watch.
func (d *cmdDeps) refuseWatch(cmd *cobra.Command) error {
	if d.modelContext(cmd) {
		return errWatchModel
	}
	if agent := d.AgentAncestor(); agent != "" {
		return fmt.Errorf("%w (a %s process runs this command)", errWatchModel, agent)
	}
	return nil
}

// watchPeer does for a peer id what peer view does with tab, else what peer
// show does. The refusal for models came before, in the command.
func (d *cmdDeps) watchPeer(cmd *cobra.Command, id string, tab bool, client, lead string, lines int) error {
	ctx, cancel, err := peerContext(d, cmd, lead, false, 0)
	if err != nil {
		return err
	}
	defer cancel()
	if tab {
		viewed, err := peer.View(ctx, id, client)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), viewed.Line())
		return err
	}
	shown, err := peer.Show(ctx, id, lines)
	if err != nil {
		return err
	}
	_, err = io.WriteString(cmd.OutOrStdout(), shown.Text())
	return err
}

// watchTab opens the tab, or without Rex prints the command to run instead.
func (d *cmdDeps) watchTab(cmd *cobra.Command, id, client string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	ctx, cancel, err := peerContext(d, cmd, "", false, 0)
	if err != nil {
		return err
	}
	defer cancel()
	viewed, err := peer.WatchTab(ctx, id, []string{exe, "watch", id}, client)
	switch {
	case errors.Is(err, peer.ErrNoRex):
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "no Rex here; run in your own terminal: duolog watch %s\n", id)
	case err == nil:
		_, err = fmt.Fprintln(cmd.OutOrStdout(), viewed.Line())
	}
	return err
}

// runDelegateRecording is runDelegate for a run that has a record at path: the
// run writes its worker, model and log into the record when the worker starts.
func (d *cmdDeps) runDelegateRecording(path string) delegateFunc {
	return func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		if d.RunDelegate != nil {
			return d.RunDelegate(ctx, r)
		}
		return delegate.Run(ctx, recordingDeps(d.Delegate, path), r)
	}
}

// recordingDeps is deps with each worker wrapped, so that a worker that starts
// puts its agent, model and log path in the running record at path first. The
// worker itself runs and logs as before. It changes no map of deps.
func recordingDeps(deps delegate.Deps, path string) delegate.Deps {
	agents := make(map[string]delegate.Agent, len(deps.Agents))
	for name, a := range deps.Agents {
		if a.Worker != nil {
			a.Worker = recordingWorker{Worker: a.Worker, path: path}
		}
		agents[name] = a
	}
	deps.Agents = agents
	return deps
}

// recordingWorker records its worker's start; see recordingDeps.
type recordingWorker struct {
	delegate.Worker
	path string
}

func (w recordingWorker) Run(ctx context.Context, s corerun.Spec) (corerun.Result, error) {
	recordStart(w.path, s)
	return w.Worker.Run(ctx, s)
}

// recordStart puts the worker of s in the running record at path. It is best
// effort: a record that is missing or no longer running stays as it is, and a
// failed write only leaves watch without a log to follow.
func recordStart(path string, s corerun.Spec) {
	rec, err := delegate.ReadRecord(path, nil)
	if err != nil || rec.State != "running" {
		return
	}
	log, err := filepath.Abs(filepath.Join(s.TaskDir, "stdout.log"))
	if err != nil {
		return
	}
	if rel, err := filepath.Rel(rec.WorkRoot, log); err == nil && filepath.IsLocal(rel) {
		log = filepath.ToSlash(rel)
	}
	rec.Agent, rec.Model, rec.Log = s.Route.Agent, s.Route.Model, log
	_ = delegate.WriteRecord(path, rec)
}
