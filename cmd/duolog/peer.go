package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/peer"
	"github.com/spf13/cobra"
)

// peerContext refuses workers before it reads anything: a worker never drives
// a terminal.
func peerContext(cmd *cobra.Command, lead string, allowManual bool, timeout time.Duration) (context.Context, context.CancelFunc, error) {
	if isWorker() {
		return nil, nil, errors.New("workers cannot open or control peer sessions")
	}
	root, err := projectRoot()
	if err != nil {
		return nil, nil, err
	}
	cfg, err := config.Load(root)
	if err != nil {
		return nil, nil, err
	}
	ctx := peer.WithOptions(cmd.Context(), peer.Options{Root: root, Config: cfg, Lead: lead, AllowManualModel: allowManual})
	if timeout > 0 {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		return ctx, cancel, nil
	}
	return ctx, func() {}, nil
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
	return strings.Join(strings.Fields(line), " ")
}

func newPeerCommand() *cobra.Command {
	var lead string
	var allowManual bool
	var timeout time.Duration
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
			ctx, cancel, err := peerContext(cmd, lead, allowManual, 0)
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
		e, err := peer.Open(ctx, args[0])
		if err != nil {
			return "", err
		}
		return entryLine(e), nil
	})
	open.Flags().BoolVar(&allowManual, "allow-manual-model", false, "Allow models reserved for manual selection")
	wait := &cobra.Command{Use: "wait <id>", Short: "Keep waiting for a session after an answer or a task", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel, err := peerContext(cmd, lead, allowManual, timeout)
		if err != nil {
			return err
		}
		defer cancel()
		e, err := peer.Wait(ctx, args[0])
		if errors.Is(err, context.DeadlineExceeded) && e.ID != "" {
			// Report where the session stands; the pane stays open.
			e.Note, err = fmt.Sprintf("still %s after waiting %s", e.State, timeout), nil
		}
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), entryLine(e))
		return err
	}}
	wait.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, "Longest time to wait for the session")
	send := run("send <id> <task>", "Send a one-line reference to a task file to a ready session", cobra.ExactArgs(2), func(ctx context.Context, _ *cobra.Command, args []string) (string, error) {
		err := peer.Send(ctx, args[0], args[1])
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
	answer := run("answer <id> <option>", "Answer the open question of a session by option label or number", cobra.ExactArgs(2), func(ctx context.Context, _ *cobra.Command, args []string) (string, error) {
		if err := peer.Answer(ctx, args[0], args[1]); err != nil {
			return "", err
		}
		st, err := peer.Status(ctx, args[0])
		return st.Line, err
	})
	closeCmd := run("close <id>", "Close a session; changed files are left as they are", cobra.ExactArgs(1), func(ctx context.Context, _ *cobra.Command, args []string) (string, error) {
		if err := peer.Close(ctx, args[0]); err != nil {
			return "", err
		}
		return args[0] + " closed", nil
	})
	root.AddCommand(open, wait, send, status, answer, closeCmd, newPeerLaunchCommand())
	return root
}

// newPeerLaunchCommand starts a program with extra variables and without the
// Rex ones. Windows has no env command, so a pane there runs this instead.
func newPeerLaunchCommand() *cobra.Command {
	return &cobra.Command{Use: "launch NAME=VALUE... -- program [args...]", Short: "Start a program for a peer pane", Hidden: true, DisableFlagParsing: true, RunE: func(cmd *cobra.Command, args []string) error {
		split := -1
		for i, a := range args {
			if a == "--" {
				split = i
				break
			}
		}
		if split < 0 || split == len(args)-1 {
			return errors.New("usage: duolog peer launch NAME=VALUE... -- program [args...]")
		}
		set := args[:split]
		child := exec.CommandContext(cmd.Context(), args[split+1], args[split+2:]...)
		child.Env = launchEnv(os.Environ(), set)
		child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
		err := child.Run()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return &hookExit{code: exit.ExitCode()}
		}
		return err
	}}
}

// launchEnv drops REX_SESSION and REX_BLOCK and any name that is set again.
func launchEnv(base, set []string) []string {
	skip := map[string]bool{"REX_SESSION": true, "REX_BLOCK": true}
	for _, pair := range set {
		name, _, _ := strings.Cut(pair, "=")
		skip[name] = true
	}
	var env []string
	for _, pair := range base {
		if name, _, _ := strings.Cut(pair, "="); !skip[name] {
			env = append(env, pair)
		}
	}
	return append(env, set...)
}
