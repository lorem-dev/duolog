package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/peer"
	corerun "github.com/lorem-dev/duolog/internal/core/run"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/wiring"
	"github.com/spf13/cobra"
)

// watchPoll is the wait between reads of a run's log.
const watchPoll = 300 * time.Millisecond

// errWatchModel is the refusal when a model may read the output of watch.
var errWatchModel = errors.New("duolog watch prints a worker's stream only to your own terminal, so no model reads it; run it in a terminal outside the agent, or use duolog watch --tab when you ask for it")

// errTailModel is the refusal when a model may read the output of runs tail.
var errTailModel = errors.New("duolog runs tail prints a worker's stream only for the Claude pane (/duolog-pane) or your own terminal, so no model reads it; open /duolog-pane in Claude Code, or run it in a terminal outside the agent")

// errWorkerRuns is the refusal for a worker: a worker neither lists nor
// follows delegate runs.
var errWorkerRuns = errors.New("workers cannot list or watch delegate runs")

// runsRegistry is the lead home registry of this project and the project's
// root, after the worker check.
func runsRegistry() (reg *session.Registry, root string, err error) {
	if isWorker() {
		return nil, "", errWorkerRuns
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

// runsLead is the id and bus address of the lead whose runs are listed: the
// explicit lead, DUOLOG_LEAD, or the lead that runs the command. Both are ""
// when no lead resolves and none was asked for (no live lead, or several);
// then every record is listed. A lead that was asked for and does not
// resolve, or a registry that cannot be read, is an error: the list never
// widens.
func runsLead(reg *session.Registry, lead string) (id, address string, err error) {
	l, err := reg.ResolveLead(lead)
	if err == nil {
		return l.ID, session.Address(l), nil
	}
	return "", "", runsLeadMiss(reg, lead, err)
}

// runsLeadMiss is the error of runsLead for a lead that did not resolve with
// err: nil when none was asked for and the registry reads, so that every
// record is listed.
func runsLeadMiss(reg *session.Registry, lead string, err error) error {
	if lead != "" || session.CurrentLead(os.Environ()) != "" {
		return fmt.Errorf("lead of the runs: %w", err)
	}
	if _, rerr := reg.Entries(); rerr != nil {
		return rerr
	}
	return nil
}

// listRuns returns the delegate records of the lead with this id or bus
// address, from every work project that shares the lead home, newest first.
// Both "" list all of them. A running one whose process is gone reads as
// lost. duolog runs, watch and the MCP status tool read the same list. The
// delegations folder of another work project that cannot be read is left
// out and returned in unread; root's own is an error.
func listRuns(reg *session.Registry, root, leadID, address string) (out []delegate.LocatedRecord, unread []delegate.UnreadFolder, err error) {
	records, unread, err := delegate.LeadRecordsReadable(reg, root, "")
	if err != nil || (leadID == "" && address == "") {
		return records, unread, err
	}
	for _, r := range records {
		if (leadID != "" && r.Lead == leadID) || (address != "" && r.Lead == address) {
			out = append(out, r)
		}
	}
	return out, unread, nil
}

// unreadRoots names the work projects of folders that could not be read.
func unreadRoots(unread []delegate.UnreadFolder) string {
	roots := make([]string, len(unread))
	for i, u := range unread {
		roots[i] = u.WorkRoot
	}
	return strings.Join(roots, ", ")
}

// pickListedRun is pickRun for watch and runs tail: a run that is not found
// while a folder could not be read may be in it, so the error says so.
func pickListedRun(records []delegate.LocatedRecord, unread []delegate.UnreadFolder, id string) (delegate.LocatedRecord, error) {
	rec, err := pickRun(records, id)
	if err != nil && len(unread) > 0 {
		return rec, fmt.Errorf("%w; the delegations of %s cannot be read: %w", err, unreadRoots(unread), unread[0].Err)
	}
	return rec, err
}

// newRunsCommand lists the project's workers: live peers, then delegate runs
// (running ones, then the last finished). It prints duolog's own data and
// never the worker's output, so a lead may run it.
func newRunsCommand(d *cmdDeps) *cobra.Command {
	var long, asJSON, snapshot, fast bool
	var lead string
	cmd := &cobra.Command{Use: "runs", Short: "List the workers of this project and of the submodules or superproject sharing its lead home", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if err := runsFlagsError(long, asJSON, snapshot, fast); err != nil {
			return err
		}
		if snapshot {
			return d.writeRunsSnapshot(cmd, lead, fast)
		}
		reg, _, err := runsRegistry()
		if err != nil {
			return err
		}
		rows, err := d.runRows(cmd, reg, lead, !asJSON)
		if err != nil {
			return err
		}
		return writeRunRows(cmd.OutOrStdout(), rows, long, asJSON, time.Now())
	}}
	cmd.Flags().BoolVar(&long, "long", false, "Print a table with kind, role, agent and model, age and worktree")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the rows as JSON: id, kind, role, agent, model, state, started, worktree, task_dir")
	cmd.Flags().BoolVar(&snapshot, "snapshot", false, "Print the lead's peers, runs, asks and load as one versioned JSON line")
	cmd.Flags().BoolVar(&fast, "fast", false, "With --snapshot, list the peers without reading their screens: a running peer is unknown")
	cmd.Flags().StringVar(&lead, "lead", "", "Registered lead session ID for the peers and delegate runs (defaults to DUOLOG_LEAD)")
	cmd.AddCommand(newRunsTailCommand(d))
	return cmd
}

// runsFlagsError is the refusal of flags of duolog runs that do not go
// together, or nil.
func runsFlagsError(long, asJSON, snapshot, fast bool) error {
	switch {
	case fast && !snapshot:
		return errors.New("--fast needs --snapshot")
	case snapshot && (long || asJSON):
		return errors.New("--snapshot cannot be used with --long or --json")
	case long && asJSON:
		return errors.New("--long and --json cannot be used together")
	}
	return nil
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
	var tab, closeTab bool
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
		id, scope, address := "", "", ""
		if len(args) == 1 {
			id = args[0]
		} else if scope, address, err = runsLead(reg, lead); err != nil {
			return err
		}
		records, unread, err := listRuns(reg, root, scope, address)
		if err != nil {
			return err
		}
		rec, err := pickListedRun(records, unread, id)
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
		return d.followRun(cmd, rec.Path, closeTab)
	}}
	cmd.Flags().BoolVar(&tab, "tab", false, "Open a read-only Rex tab that runs duolog watch and move your Rex client to it")
	cmd.Flags().BoolVar(&closeTab, "close-tab", false, "Close this pane after the run ends; only in a watch tab of the peer session")
	_ = cmd.Flags().MarkHidden("close-tab")
	cmd.Flags().StringVar(&client, "client", "", "With --tab, the Rex client to move (default: the one client that shows this session)")
	cmd.Flags().StringVar(&lead, "lead", "", "Registered lead session ID for a peer (defaults to DUOLOG_LEAD)")
	cmd.Flags().IntVar(&lines, "lines", peer.DefaultShowLines, fmt.Sprintf("For a peer, the number of lines, 1 to %d", peer.MaxShowLines))
	return cmd
}

// refuseWatch is the refusal when a model may read the output of watch.
func (d *cmdDeps) refuseWatch(cmd *cobra.Command) error {
	return d.refuseModel(cmd, errWatchModel)
}

// refuseModel returns refusal when a model may read the command's output: in
// a model's context, or under an agent's process; else nil.
func (d *cmdDeps) refuseModel(cmd *cobra.Command, refusal error) error {
	if d.modelContext(cmd) {
		return refusal
	}
	if agent := d.AgentAncestor(); agent != "" {
		return fmt.Errorf("%w (a %s process runs this command)", refusal, agent)
	}
	return nil
}

// viewerEnv names the program that shows runs tail to the person;
// viewerClaudeMod is the Claude Code pane.
const (
	viewerEnv       = "DUOLOG_VIEWER"
	viewerClaudeMod = "claude-mod"
)

// newRunsTailCommand prints one page of a delegate run's log, read as watch
// reads it, for the Claude pane that polls it, or for the person in a
// terminal. It finds the run by id as watch does.
func newRunsTailCommand(d *cmdDeps) *cobra.Command {
	var from int64
	var cursor string
	var asJSON bool
	cmd := &cobra.Command{Use: "tail <id>", Short: "Print one page of a delegate run's log, for the Claude pane or your own terminal", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		// The refusal comes before anything is read.
		if err := d.refuseTail(cmd); err != nil {
			return err
		}
		reg, root, err := runsRegistry()
		if err != nil {
			return err
		}
		records, unread, err := listRuns(reg, root, "", "")
		if err != nil {
			return err
		}
		rec, err := pickListedRun(records, unread, args[0])
		if err != nil {
			return err
		}
		if !asJSON && !cmd.Flags().Changed("from") {
			// Plain, for a person: the last lines up to the end.
			lines, err := delegate.TailLast(rec.Record, delegate.TailMaxLines)
			if err != nil {
				return err
			}
			return writeTailPage(cmd.OutOrStdout(), delegate.TailPage{Lines: lines}, false)
		}
		page, err := delegate.Tail(rec.Record, from, cursor)
		if err != nil {
			return err
		}
		return writeTailPage(cmd.OutOrStdout(), page, asJSON)
	}}
	cmd.Flags().Int64Var(&from, "from", -1, "The byte offset to read from, as the last page gave it; -1 starts in the last 64 KiB. Without it and --json, the last 200 lines up to the end are printed")
	cmd.Flags().StringVar(&cursor, "cursor", "", "The cursor the last page gave")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the page as one JSON line: offset, reset, lines, ended, state, cursor")
	return cmd
}

// writeTailPage prints a page as one JSON line, or its lines.
func writeTailPage(w io.Writer, page delegate.TailPage, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(w).Encode(page)
	}
	for _, line := range page.Lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

// refuseTail is the refusal for runs tail. A worker is always refused. The
// Claude pane passes: it sets DUOLOG_VIEWER=claude-mod and Claude Code is its
// direct parent. A model's Bash call has a shell between Claude Code and
// duolog, so the flag alone does not pass it. That rule relies on Claude
// Code's Bash wrapper not exec-ing the command; the lead guard covers a
// model's call that names runs tail either way. Anyone else is refused as
// for watch, with the refusal of runs tail.
func (d *cmdDeps) refuseTail(cmd *cobra.Command) error {
	if isWorker() {
		return errWorkerRuns
	}
	if os.Getenv(viewerEnv) == viewerClaudeMod && d.ParentAgent() == wiring.ClaudeName() {
		return nil
	}
	return d.refuseModel(cmd, errTailModel)
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

// followRun prints the run at path until it ends, then closes the tab when
// closeTab is set.
func (d *cmdDeps) followRun(cmd *cobra.Command, path string, closeTab bool) error {
	if err := delegate.Watch(cmd.Context(), cmd.OutOrStdout(), path, session.ProcessAlive, watchPoll); err != nil || !closeTab {
		return err
	}
	d.closeWatchTab(cmd)
	return nil
}

// closeWatchTab closes the pane the command runs in once the run has ended,
// after the linger. A cancelled command (ctrl+c) keeps its pane, and a failed
// close prints nothing more: the person can close the tab.
func (d *cmdDeps) closeWatchTab(cmd *cobra.Command) {
	ctx := cmd.Context()
	if ctx == nil || ctx.Err() != nil {
		return
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "this tab closes in 10 s")
	if d.watchTabLinger > 0 {
		select {
		case <-time.After(d.watchTabLinger):
		case <-ctx.Done():
			return
		}
	}
	pctx, cancel, err := peerContext(d, cmd, "", false, 0)
	if err != nil {
		return
	}
	defer cancel()
	_ = peer.CloseWatchTab(pctx)
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
	viewed, err := peer.WatchTab(ctx, id, []string{exe, "watch", id, "--close-tab"}, client)
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
