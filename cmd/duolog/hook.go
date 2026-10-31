package main

import (
	"errors"
	"io"
	"os"
	"time"

	"github.com/lorem-dev/duolog/internal/hook"
	"github.com/spf13/cobra"
)

type hookExit struct{ code int }

func (e *hookExit) Error() string { return "hook wake" }

// needsExit reports that the worker or the capability check needs the lead to
// act. It is not a failure, so it has its own exit code.
type needsExit struct{}

func (e *needsExit) Error() string { return "needs lead" }

func newHookCommand() *cobra.Command {
	var agent string
	cmd := &cobra.Command{Use: "hook <event>", Short: "Deliver messages to a lead hook", SilenceErrors: true, RunE: func(cmd *cobra.Command, args []string) error {
		defer func() { _ = recover() }()
		started := time.Now()
		// Hook failures must never fail the user's session or print diagnostics.
		if len(args) != 1 {
			return nil
		}
		root, err := projectRoot()
		if err != nil {
			return nil
		}
		data, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 4*1024*1024+1))
		if err != nil || len(data) > 4*1024*1024 {
			return nil
		}
		env := hook.Env{StartedAt: started, Root: root, WorkRoot: workRoot(root), Agent: agent, Input: data, Environ: os.Environ(), Stderr: cmd.ErrOrStderr()}
		var out hook.Output
		switch args[0] {
		case "SessionStart", "session-start":
			out = hook.SessionStart(cmd.Context(), env)
		case "UserPromptSubmit", "prompt":
			out = hook.Prompt(cmd.Context(), env)
		case "PostToolUse", "post-tool":
			out = hook.PostTool(cmd.Context(), env)
		case "SessionEnd", "session-end":
			out = hook.SessionEnd(cmd.Context(), env)
		case "PreToolUse", "PermissionRequest":
			out = hook.Observe(cmd.Context(), env)
		case "wake", "Wake":
			if code := hook.Wake(cmd.Context(), env); code != 0 {
				return &hookExit{code: code}
			}
			return nil
		default:
			return nil
		}
		_, _ = cmd.OutOrStdout().Write(out.Stdout)
		_, _ = cmd.ErrOrStderr().Write(out.Stderr)
		return nil
	}}
	cmd.Flags().StringVar(&agent, "agent", "", "Agent receiving this hook")
	return cmd
}

func exitCode(err error) int {
	var wake *hookExit
	if errors.As(err, &wake) {
		return wake.code
	}
	var needs *needsExit
	if errors.As(err, &needs) {
		return 3
	}
	return 1
}
