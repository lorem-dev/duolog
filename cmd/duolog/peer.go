package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/app/peer"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/wiring"
	"github.com/lorem-dev/duolog/internal/workerenv"
	"github.com/spf13/cobra"
)

// peerContext is peerCtx for a cobra command: the command's context is the
// parent.
func peerContext(d *cmdDeps, cmd *cobra.Command, lead string, allowManual bool, timeout time.Duration) (context.Context, context.CancelFunc, error) {
	ctx, done, err := d.peerCtx(cmd.Context(), lead, allowManual, timeout)
	return ctx, done, err
}

// peerCtx refuses workers before it reads anything: a worker never drives a
// terminal. It returns parent with the peer options of this project, which
// the CLI and the MCP server's peer tool both use.
func (d *cmdDeps) peerCtx(parent context.Context, lead string, allowManual bool, timeout time.Duration) (context.Context, func(), error) {
	return d.peerCtxAt(parent, lead, allowManual, timeout, "")
}

// peerCtxAt binds work rules and telemetry after the target checkout is known.
func (d *cmdDeps) peerCtxAt(parent context.Context, lead string, allowManual bool, timeout time.Duration, work string) (context.Context, func(), error) {
	if isWorker() {
		return nil, nil, errors.New("workers cannot open or control peer sessions")
	}
	root, err := projectRoot()
	if err != nil {
		return nil, nil, err
	}
	if work == "" {
		work = project.CurrentWorkRoot(root)
	}
	root = project.StateRoot(work)
	cfg, err := configfile.Load(root, wiring.ConfigOptions())
	if err != nil {
		return nil, nil, err
	}
	opts := peer.Options{Deps: d.peerDeps(), Root: root, WorkDir: work, Config: cfg, Lead: lead, LeadHints: hintsFrom(parent), AllowManualModel: allowManual, ProtectedRoots: launchProtectedRoots()}
	// Peer opens, sends and closes, and each task a peer ends, reach
	// telemetry through these observers; a store that cannot open records none.
	done := func() {}
	if tel := openTelemetry(cfg, root); tel != nil {
		opts.Observe, opts.ObserveAttempt = observePeer(tel), observeAttempt(tel, cfg)
		done = func() { _ = tel.Close() }
	}
	ctx := peer.WithOptions(parent, opts)
	if timeout > 0 {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		return ctx, func() { cancel(); done() }, nil
	}
	return ctx, done, nil
}

// peerCtxForID is peerCtxAt in the checkout of peer id, or in the caller's
// when the peer has none; the lead hints of an MCP call in parent go with it.
func (d *cmdDeps) peerCtxForID(parent context.Context, lead string, allowManual bool, timeout time.Duration, id string) (context.Context, func(), error) {
	if isWorker() {
		return nil, nil, errors.New("workers cannot open or control peer sessions")
	}
	root, err := projectRoot()
	if err != nil {
		return nil, nil, err
	}
	opts := peer.Options{Root: root, Lead: lead, LeadHints: hintsFrom(parent)}
	work, err := peer.WorkRoot(peer.WithOptions(parent, opts), id)
	if err != nil {
		return d.peerCtx(parent, lead, allowManual, timeout)
	}
	return d.peerCtxAt(parent, lead, allowManual, timeout, work)
}

func (d *cmdDeps) peerCommandContext(cmd *cobra.Command, lead string, allowManual bool, use string, args []string) (context.Context, func(), error) {
	switch strings.Fields(use)[0] {
	case "send", "status", "answer", "close":
		return d.peerCtxForID(cmd.Context(), lead, allowManual, 0, args[0])
	default:
		return d.peerCtx(cmd.Context(), lead, allowManual, 0)
	}
}

// sendPeerTask places a CLI ref in the caller's checkout before work binding.
func sendPeerTask(ctx context.Context, id, ref string) error {
	path, err := filepath.Abs(strings.TrimSpace(ref))
	if err != nil {
		return err
	}
	return peer.Send(ctx, id, path)
}

// launchProtectedRoots names the telemetry and balance roots for a worker's or
// peer's launch settings, collection on or off. Roots that cannot be placed
// are left out: the worker gate then fails closed on every action.
func launchProtectedRoots() []string {
	roots, err := gateProtectedRoots()
	if err != nil {
		return nil
	}
	return roots
}

// entryLine is the one line a lead reads after open or wait. A run here line is
// printed as it is, because that is what the lead acts on.
func entryLine(e peer.Entry) string {
	if strings.HasPrefix(e.Note, "run here: ") {
		return e.Note
	}
	line := e.ID + " " + string(e.State)
	switch {
	case e.Question != "":
		line += ": " + e.Question
	case e.Note != "":
		line += ": " + e.Note
	}
	if e.Report != "" {
		line += "; report " + e.Report
	}
	line = strings.Join(strings.Fields(line), " ")
	if e.Excerpt != "" {
		// The start of the peer's report keeps its line breaks.
		line += "\n" + e.Excerpt
	}
	return line
}

// openLine opens a peer for role, a second one when fresh, and returns its line.
func openLine(ctx context.Context, role string, fresh bool) (string, error) {
	open := peer.Open
	if fresh {
		open = peer.OpenNew
	}
	e, err := open(ctx, role)
	if err != nil {
		return "", err
	}
	return entryLine(e), nil
}

// peerWaitFlags are the flags of `duolog peer wait`.
type peerWaitFlags struct {
	timeout  time.Duration
	afterAsk int64
}

func (w *peerWaitFlags) bind(cmd *cobra.Command) {
	cmd.Flags().DurationVar(&w.timeout, "timeout", 30*time.Minute, "Longest time to wait for the session")
	cmd.Flags().Int64Var(&w.afterAsk, "after-ask", 0, "Keep waiting through this relayed gate ask's answer or expiry")
}

func (w *peerWaitFlags) wait(ctx context.Context, id string) (peer.Entry, error) {
	if w.afterAsk < 0 {
		return peer.Entry{}, errors.New("--after-ask must be a positive ask number")
	}
	return peer.WaitAfterAsk(ctx, id, w.afterAsk)
}

func newPeerCommand(d *cmdDeps) *cobra.Command {
	var lead string
	var allowManual, openNew bool
	var waitFlags peerWaitFlags
	root := &cobra.Command{Use: "peer", Short: "Open and drive a second agent session in a Rex pane"}
	if runtime.GOOS == "windows" {
		root.Args = cobra.ArbitraryArgs
		root.DisableFlagParsing = true
		root.RunE = func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), peer.WindowsNotice)
			return err
		}
		return root
	}
	root.PersistentFlags().StringVar(&lead, "lead", "", "Registered lead session ID (defaults to DUOLOG_LEAD)")
	run := func(use, short string, args cobra.PositionalArgs, call func(ctx context.Context, cmd *cobra.Command, args []string) (string, error)) *cobra.Command {
		return &cobra.Command{Use: use, Short: short, Args: args, RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel, err := d.peerCommandContext(cmd, lead, allowManual, use, args)
			if err != nil {
				return err
			}
			defer cancel()
			line, err := call(ctx, cmd, args)
			if line != "" {
				if _, werr := fmt.Fprintln(cmd.OutOrStdout(), line); werr != nil {
					return errors.Join(err, werr)
				}
			}
			return err
		}}
	}
	open := run("open <role>", "Open a session for a role and wait until it is ready or asks a question", cobra.ExactArgs(1), func(ctx context.Context, _ *cobra.Command, args []string) (string, error) {
		return openLine(ctx, args[0], openNew)
	})
	open.Flags().BoolVar(&allowManual, "allow-manual-model", false, "Allow models reserved for manual selection")
	open.Flags().BoolVar(&openNew, "new", false, "Open another session of the role even when one is live in this checkout")
	wait := &cobra.Command{Use: "wait <id>", Short: "Keep waiting for a session after an answer or a task", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel, err := d.peerCtxForID(cmd.Context(), lead, allowManual, waitFlags.timeout, args[0])
		if err != nil {
			return err
		}
		defer cancel()
		e, err := waitFlags.wait(ctx, args[0])
		if errors.Is(err, context.DeadlineExceeded) && e.ID != "" {
			// Report where the session stands; the pane stays open.
			e.Note, err = fmt.Sprintf("still %s after waiting %s", e.State, waitFlags.timeout), nil
		}
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), entryLine(e))
		return err
	}}
	waitFlags.bind(wait)
	send := run("send <id> <task>", "Send a one-line reference to a task file to a ready session", cobra.ExactArgs(2), func(ctx context.Context, _ *cobra.Command, args []string) (string, error) {
		err := sendPeerTask(ctx, args[0], args[1])
		var here *peer.RunHereError
		if errors.As(err, &here) {
			return here.Line, nil
		}
		if err != nil {
			return "", err
		}
		return args[0] + " sent " + args[1], nil
	})
	status := run("status <id>", "Show the state of a session in one line", cobra.ExactArgs(1), func(ctx context.Context, _ *cobra.Command, args []string) (string, error) {
		st, err := peer.Status(ctx, args[0])
		return st.Line, err
	})
	var reason string
	answer := run("answer <id> <option>", "Answer the open question of a session by option label or number", cobra.ExactArgs(2), func(ctx context.Context, _ *cobra.Command, args []string) (string, error) {
		if err := peer.AnswerWithReason(ctx, args[0], args[1], reason); err != nil {
			return "", err
		}
		st, err := peer.Status(ctx, args[0])
		return st.Line, err
	})
	answer.Flags().StringVar(&reason, "reason", "", "One line to type into the pane, after \"Reason: \", when the answer leaves it interrupted, such as why a permission was denied")
	closeCmd := run("close <id>", "Close a session; changed files are left as they are", cobra.ExactArgs(1), d.peerClose)
	var listJSON, listLong bool
	list := &cobra.Command{Use: "list", Short: "List this lead's peer states without answering questions", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if listLong && listJSON {
			return errors.New("--long and --json cannot be used together")
		}
		ctx, cancel, err := peerContext(d, cmd, lead, false, 0)
		if err != nil {
			return err
		}
		defer cancel()
		peers, err := peer.List(ctx)
		if err != nil {
			return err
		}
		if listJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(peers)
		}
		return writePeerList(ctx, d, cmd.OutOrStdout(), peers, listLong)
	}}
	list.Flags().BoolVar(&listJSON, "json", false, "Print peers as JSON; id and state keep their old values")
	list.Flags().BoolVar(&listLong, "long", false, "Print a table with role, agent, task, last event and tab")
	release := run("release <id>", "End the account hold of a pane that no session records, as quota and doctor show it", cobra.ExactArgs(1), func(ctx context.Context, _ *cobra.Command, args []string) (string, error) {
		return peer.Release(ctx, args[0])
	})
	root.AddCommand(open, wait, send, status, answer, closeCmd, list, release, newPeerQueueCommand(d, &lead), newPeerShowCommand(d, &lead), newPeerViewCommand(d, &lead), newPeerLaunchCommand(d))
	return root
}

// peerClose closes a peer and says so when its pane was already gone. The
// freed session goes to the lead's queue: a detached run opens what it can.
func (d *cmdDeps) peerClose(ctx context.Context, _ *cobra.Command, args []string) (string, error) {
	id := args[0]
	note, err := peer.CloseNote(ctx, id)
	if err != nil {
		return "", err
	}
	line := id + " closed"
	if note != "" {
		line += "; " + note
	}
	// The close just freed a session: every queued item may take it.
	switch started, err := d.StartQueueRun(ctx, nil); {
	case err != nil:
		line += "; queue run not started: " + err.Error()
	case started:
		line += "; queue run started"
	}
	return line, nil
}

// errPeerShowModel is the refusal when a model may read the output.
var errPeerShowModel = errors.New("duolog peer show prints a peer's screen only to your own terminal, so no model reads it; run it in a terminal outside the agent, or use duolog peer view <id>")

// newPeerShowCommand prints the last lines of a peer's pane to the person's
// terminal. It refuses before any Rex call whenever a model may read the
// output, and it has no flag that overrides the refusal. The checks guard
// against accidents; no check can prove that a terminal is the person's.
// The lead hook also stops a lead's tool call that names it (peerShowLeadGuard).
func newPeerShowCommand(d *cmdDeps, lead *string) *cobra.Command {
	var lines int
	cmd := &cobra.Command{Use: "show <id>", Short: "Print the last lines of a peer's screen to your own terminal", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if d.modelContext(cmd) {
			return errPeerShowModel
		}
		// A pty and a cleaned environment pass the check above, as under
		// script or in a new Rex or tmux pane; an agent process above
		// this one still shows.
		if agent := d.AgentAncestor(); agent != "" {
			return fmt.Errorf("%w (a %s process runs this command)", errPeerShowModel, agent)
		}
		ctx, cancel, err := peerContext(d, cmd, *lead, false, 0)
		if err != nil {
			return err
		}
		defer cancel()
		shown, err := peer.Show(ctx, args[0], lines)
		if err != nil {
			return err
		}
		_, err = io.WriteString(cmd.OutOrStdout(), shown.Text())
		return err
	}}
	cmd.Flags().IntVar(&lines, "lines", peer.DefaultShowLines, fmt.Sprintf("Number of lines, 1 to %d", peer.MaxShowLines))
	return cmd
}

// recordLauncher records this process on the account hold of its pane, which
// the DUOLOG_PEER of the pane's variables names, so that a peer open that dies
// before it records the pane leaves the pane's presence behind. A pane
// without a peer id holds nothing.
func recordLauncher(ctx context.Context, d *cmdDeps, set []string) error {
	id := ""
	for _, pair := range set {
		if v, ok := strings.CutPrefix(pair, "DUOLOG_PEER="); ok {
			id = v
		}
	}
	if id == "" {
		return nil
	}
	st, err := d.OpenLaunchState()
	if err != nil {
		return err
	}
	return st.RecordLauncher(ctx, id, paidruns.ProcessOwner(os.Getpid()))
}

// newPeerLaunchCommand starts a peer's program with exactly the variables in its
// file, plus what a terminal program needs from the pane. It records itself on
// the pane's account hold, then deletes the file, then starts the program: a
// file still there proves that nothing started.
func newPeerLaunchCommand(d *cmdDeps) *cobra.Command {
	var envFile string
	cmd := &cobra.Command{Use: "launch --env-file FILE -- program [args...]", Short: "Start a program for a peer pane", Hidden: true, Args: cobra.MinimumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		data, err := os.ReadFile(envFile)
		if err != nil {
			return err
		}
		var set []string
		if err := json.Unmarshal(data, &set); err != nil {
			return fmt.Errorf("%s: %w", envFile, err)
		}
		// The program is the one the admitted environment finds on its own
		// PATH, as a worker's is, never the one this launcher's PATH picks.
		// Not found starts nothing and leaves the file to take.
		bin, err := workerenv.LookPath(args[0], set)
		if err != nil {
			return fmt.Errorf("peer launch: %w", err)
		}
		if err := recordLauncher(cmd.Context(), d, set); err != nil {
			return fmt.Errorf("peer launch: %w", err)
		}
		if err := os.Remove(envFile); err != nil {
			return err
		}
		child := exec.CommandContext(cmd.Context(), bin, args[1:]...)
		child.Args[0] = args[0]
		child.Env = launchEnv(os.Environ(), set)
		child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
		err = child.Run()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return &hookExit{code: exit.ExitCode()}
		}
		return err
	}}
	cmd.Flags().StringVar(&envFile, "env-file", "", "File that holds the variables, as a JSON list of NAME=VALUE")
	_ = cmd.MarkFlagRequired("env-file")
	return cmd
}

// launchEnv keeps what a terminal program needs from the pane and adds the listed
// variables. env_passthrough is the only other way in, as for a one-shot worker.
func launchEnv(base, set []string) []string {
	var env []string
	for _, pair := range base {
		name, _, _ := strings.Cut(pair, "=")
		if slices.Contains(paneVariables, name) || strings.HasPrefix(name, "LC_") {
			env = append(env, pair)
		}
	}
	return append(env, set...)
}

var paneVariables = []string{"TERM", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "COLORTERM", "COLUMNS", "LINES", "LANG", "LANGUAGE", "TMPDIR", "TZ", "SHELL"}

// newPeerViewCommand moves the person's Rex client to a peer's existing tab.
// It prints one line and never the screen.
func newPeerViewCommand(d *cmdDeps, lead *string) *cobra.Command {
	var client string
	view := &cobra.Command{Use: "view <id>", Short: "Show a peer's tab on your Rex client; look, do not type", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel, err := peerContext(d, cmd, *lead, false, 0)
		if err != nil {
			return err
		}
		defer cancel()
		viewed, err := peer.View(ctx, args[0], client)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), viewed.Line())
		return err
	}}
	view.Flags().StringVar(&client, "client", "", "Rex client to move (default: the one client that shows this session)")
	return view
}
