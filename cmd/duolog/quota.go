package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	appquota "github.com/lorem-dev/duolog/internal/app/quota"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/spf13/cobra"
)

func newQuotaCommand(d *cmdDeps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "quota", Short: "Read subscription quota without a model call", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := projectRoot()
		if err != nil {
			return err
		}
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
			return err
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
			return err
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
		results, read := appquota.Collect(cmd.Context(), req)
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
