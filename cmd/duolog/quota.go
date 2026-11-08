package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	appquota "github.com/lorem-dev/duolog/internal/app/quota"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/wiring"
	"github.com/spf13/cobra"
)

func newQuotaCommand(d *cmdDeps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "quota", Short: "Read subscription quota without a model call", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := projectRoot()
		if err != nil {
			return err
		}
		results, read, err := collectQuota(cmd.Context(), d, root)
		if err != nil {
			return err
		}
		if asJSON {
			err = json.NewEncoder(cmd.OutOrStdout()).Encode(results)
		} else {
			err = appquota.Print(cmd.OutOrStdout(), results, time.Now())
		}
		if err != nil {
			return err
		}
		if read == 0 {
			return errors.New("no subscription quota available")
		}
		return nil
	}}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print quota as one JSON object")
	return cmd
}

// quotaShort is the quota in one short line for the MCP status tool: each
// agent's plan and its 5-hour and weekly use, "" when no quota was read.
func quotaShort(results map[string]appquota.Result) string {
	var parts []string
	for _, d := range wiring.Descriptors() {
		agent := d.Name
		r, ok := results[agent]
		if !ok || r.Unavailable != "" || r.Snapshot == nil || len(r.Windows) == 0 {
			continue
		}
		part := agent
		if r.Plan != "" {
			part += " " + r.Plan
		}
		for _, w := range r.Windows {
			switch w.Kind {
			case "session":
				part += fmt.Sprintf(" 5h %g%%", w.UsedPercent)
			case "weekly_all":
				part += fmt.Sprintf(" weekly %g%%", w.UsedPercent)
			}
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "; ")
}

// collectQuota reads the quota of the project at root as `duolog quota`
// does, and says how many agents it read.
func collectQuota(ctx context.Context, d *cmdDeps, root string) (map[string]appquota.Result, int, error) {
	{
		// Read-only unless a mark is cleared: never create .duolog for a lookup.
		var reg *session.Registry
		var regErr error
		var unavailable map[string]bool
		if info, statErr := os.Stat(filepath.Join(root, ".duolog")); statErr == nil && info.IsDir() {
			reg, regErr = session.Open(filepath.Join(root, ".duolog"))
		}
		if reg != nil && regErr == nil {
			unavailable, regErr = reg.Unavailable()
		}
		// The free reads use the person's config, so the worker's process, its
		// proxy and CA variables included, is the one read.
		cfg, err := configfile.Load(root)
		if err != nil {
			return nil, 0, err
		}
		// Telemetry mirrors what this read finds and never reads a vendor
		// itself.
		var observe balanceObserver
		if tel := openAccountTelemetry(cfg, root); tel != nil {
			defer tel.Close()
			observe = observeBalance(tel)
		}
		gate, err := d.Delegate.PaidGate(cfg)
		if err != nil {
			return nil, 0, err
		}
		home, _ := os.UserHomeDir()
		req := appquota.Request{Gate: gate, Config: cfg, Home: home, Root: root, GOOS: runtime.GOOS,
			Environ: os.Environ(), Unavailable: unavailable, RegistryErr: regErr, Observe: observe.Quota,
			// The lead only from an observation of it, reported and never run.
			Lead: func(agent string) (corelaunch.ProcessInput, bool) {
				observed, _ := d.ObserveLead(agent)
				return session.LeadInput(cfg, observed, home, root)
			}}
		if reg != nil {
			req.Marks = reg
		}
		results, read := appquota.Collect(ctx, req)
		return results, read, nil
	}
}
