package main

import (
	"context"
	"fmt"
	"io"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	calibration "github.com/lorem-dev/duolog/internal/app/calibrate"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/policy"
	"github.com/lorem-dev/duolog/internal/wiring"
)

// printBlockedProjectChecks prints the lines of blockedProjectChecks to w.
func printBlockedProjectChecks(ctx context.Context, w io.Writer, root string) error {
	for _, line := range blockedProjectChecks(ctx, root) {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

// blockedProjectChecks lists, as lines to print, the project's own test and
// lint commands (the survey calibrate does) that the gate would not allow for
// the implement role. It only reports: nothing is allowed or changed. A project
// that has no commit, no config or no known check gives no lines.
func blockedProjectChecks(ctx context.Context, root string) []string {
	commands, err := calibration.ProjectChecks(ctx, root)
	if err != nil || len(commands) == 0 {
		return nil
	}
	cfg, err := configfile.Load(root, wiring.ConfigOptions())
	if err != nil {
		return nil
	}
	const role = "implement"
	rules := policy.LeadRules{RoleSandbox: "read-only"}
	if r, ok := cfg.Roles[role]; ok {
		rules.RoleSandbox = r.Sandbox
	}
	var lines []string
	in := policy.PreflightInput{Policy: cfg.Policy, Lead: rules, Mode: mode.Edits, Role: role, Work: root, State: root, ProtectedRoots: launchProtectedRoots()}
	for _, b := range policy.Preflight(in, commands) {
		line := fmt.Sprintf("note: the project check %q is %s for the %s role, so a worker would stop on it", b.Command, b.Decision, role)
		if b.Decision == policy.Ask {
			line += "; the person may approve it with " + delegate.RememberHint(b.Command)
		}
		lines = append(lines, line)
	}
	return lines
}
