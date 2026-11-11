package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	runsnapapp "github.com/lorem-dev/duolog/internal/app/runsnap"
	"github.com/lorem-dev/duolog/internal/core/runsnap"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/spf13/cobra"
)

// statuslineBudget is how long the status line may take. Claude Code cancels a
// slow script on the next update, and a line that arrives late is no help.
const statuslineBudget = time.Second

// statuslineMaxInput is the most of Claude Code's JSON that is read.
const statuslineMaxInput = 1 << 20

// statuslineInput is the part of Claude Code's status line JSON that duolog
// reads.
type statuslineInput struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	Workspace struct {
		CurrentDir string `json:"current_dir"`
		ProjectDir string `json:"project_dir"`
	} `json:"workspace"`
}

// dir is the folder that names the project: the project folder, else the
// current one, else the working folder of the session.
func (in statuslineInput) dir() string {
	for _, d := range []string{in.Workspace.ProjectDir, in.Workspace.CurrentDir, in.Cwd} {
		if d != "" {
			return d
		}
	}
	return ""
}

// runStatusline reads Claude Code's status line JSON from in and prints the
// first line of the lead's worker snapshot to out. It prints nothing when
// anything is amiss: the input is not JSON or over 1 MiB, it names no
// session, this process is a worker or a peer, build fails or is not done
// within budget, or there is no lead or nothing to show. build gets the
// parsed input.
func runStatusline(ctx context.Context, in io.Reader, out io.Writer, environ []string, budget time.Duration, build func(context.Context, statuslineInput) (runsnap.Snapshot, error)) {
	if statuslineWorker(environ) {
		return
	}
	data, err := io.ReadAll(io.LimitReader(in, statuslineMaxInput+1))
	if err != nil || len(data) > statuslineMaxInput {
		return
	}
	var input statuslineInput
	if json.Unmarshal(data, &input) != nil || input.SessionID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	type result struct {
		snap runsnap.Snapshot
		err  error
	}
	done := make(chan result, 1)
	go func() {
		var r result
		defer func() {
			if recover() != nil {
				r = result{err: context.Canceled}
			}
			done <- r
		}()
		r.snap, r.err = build(ctx, input)
	}()
	select {
	case <-ctx.Done():
	case r := <-done:
		if r.err != nil || ctx.Err() != nil || r.snap.Lead == nil {
			return
		}
		if line := r.snap.Line(); line != "" {
			_, _ = io.WriteString(out, line+"\n")
		}
	}
}

// statuslineWorker reports whether environ is a worker's or a peer's: a status
// line shows the lead's workers, so it shows nothing to them.
func statuslineWorker(environ []string) bool {
	for _, kv := range environ {
		name, value, _ := strings.Cut(kv, "=")
		switch name {
		case "DUOLOG_DEPTH":
			if depth, _ := strconv.Atoi(value); depth >= 1 {
				return true
			}
		case "DUOLOG_PEER":
			if value != "" {
				return true
			}
		}
	}
	return false
}

// statuslineSnapshot is the fast snapshot of the lead whose session is
// in.SessionID, for the work project in names. It creates no state.
func (d *cmdDeps) statuslineSnapshot(ctx context.Context, in statuslineInput) (runsnap.Snapshot, error) {
	dir := in.dir()
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return runsnap.Snapshot{}, err
		}
		dir = cwd
	}
	root := project.StateRoot(dir)
	return d.runsSnapshot(ctx, root, in.SessionID, true, time.Now(), func(reg *session.Registry) runsnapapp.Deps {
		return d.snapDepsAt(reg, root)
	})
}

// newStatuslineCommand is duolog statusline, the command of Claude Code's
// statusLine setting. It always exits 0, prints no error, no color and at most
// one line.
func newStatuslineCommand(d *cmdDeps) *cobra.Command {
	return statuslineCommand(d.statuslineSnapshot)
}

// statuslineCommand is newStatuslineCommand with build as the snapshot source.
// Flags and arguments are ignored, not parsed, so that none can fail it.
func statuslineCommand(build func(context.Context, statuslineInput) (runsnap.Snapshot, error)) *cobra.Command {
	return &cobra.Command{
		Use:                "statusline",
		Short:              "Print the lead's worker line for Claude Code's status line (reads its JSON on stdin)",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		SilenceErrors:      true,
		SilenceUsage:       true,
		Run: func(cmd *cobra.Command, _ []string) {
			defer func() { _ = recover() }()
			runStatusline(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), os.Environ(), statuslineBudget, build)
		},
	}
}
