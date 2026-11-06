package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/app/doctor"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/rex"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/telemetry"
	"github.com/lorem-dev/duolog/internal/workerenv"
	"github.com/spf13/cobra"
)

// locateRex finds the rex doctor asks for the pane inventory; tests wrap it.
var locateRex = func() (string, bool) { return rex.Locate(os.Getenv("REX_BIN_DIR"), rex.BundleGlobs()) }

func newDoctorCommand() *cobra.Command {
	var probe bool
	cmd := &cobra.Command{Use: "doctor", Short: "Report agent health, trust and availability", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := projectRoot()
		if err != nil {
			return err
		}
		cfg, err := configfile.Load(root)
		if err != nil {
			return doctorConfigError(cmd, err)
		}
		reg, err := session.Open(filepath.Join(root, ".duolog"))
		if err != nil {
			return err
		}
		opts := doctor.Options{Root: root, Registry: reg, Probe: probe}
		opts.RexShadow, _ = rex.Shadow()
		var inventoryFindings []doctor.Finding
		if bin, ok := locateRex(); ok && runtime.GOOS != "windows" {
			opts.RexBin = bin
			opts.Panes, err = doctorPanes(cmd.Context(), bin, os.Getenv("REX_SESSION"), reg)
			if err != nil {
				inventoryFindings = append(inventoryFindings, doctor.Finding{Level: "warning", Message: "Rex pane inventory unavailable: " + err.Error()})
			} else {
				inventoryFindings = append(inventoryFindings, doctor.Finding{Level: "info", Message: "Rex pane inventory checked"})
			}
		}
		for _, finding := range append(inventoryFindings, doctor.Run(cmd.Context(), doctorDeps(), cfg, opts)...) {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", finding.Level, finding.Message); err != nil {
				return err
			}
		}
		_, err = fmt.Fprint(cmd.OutOrStdout(), doctorTelemetry(cmd, cfg, root))
		return err
	}}
	cmd.Flags().BoolVar(&probe, "probe", false, "Make a minimal model call from an empty temporary folder")
	return cmd
}

// doctorConfigError reports a config that does not load as doctor's first
// line, with its file and line. Every other check needs the config, so doctor
// stops there and fails.
func doctorConfigError(cmd *cobra.Command, err error) error {
	if _, werr := fmt.Fprintf(cmd.OutOrStdout(), "error: %s; hooks and the gate cannot load their rules\n", configfile.Problem(err)); werr != nil {
		return werr
	}
	return errors.New("the config does not load; fix it and run duolog doctor again")
}

// doctorTelemetry checks the telemetry store for doctor. It creates no root
// and no salt: a missing root reports the settings only. A model reading the
// output gets the coarse line alone.
func doctorTelemetry(cmd *cobra.Command, cfg *config.Config, root string) string {
	o := telemetryOptions(cfg, root)
	o.CanonicalStateRoot = ""
	st := telemetry.Status{Enabled: cfg.Telemetry.Enabled, ProjectEnabled: cfg.Telemetry.ProjectEnabled, Protection: telemetry.Protection{Refusal: "none"}}
	path, err := telemetry.ResolveRoot(o.Location)
	if err == nil {
		if _, statErr := os.Lstat(path); os.IsNotExist(statErr) {
			o.Enabled = false
		}
		var s *telemetry.Store
		if s, err = telemetry.Open(o); err == nil {
			defer s.Close()
			var got telemetry.Status
			got, err = s.Status()
			got.Enabled, got.ProjectEnabled = st.Enabled, st.ProjectEnabled
			st = got
		}
	}
	if err != nil && st.Protection.Refusal == "none" {
		// The store's errors are closed names; a refusal names its kind.
		st.Protection.Refusal = "other"
		if kind, ok := strings.CutPrefix(err.Error(), "telemetry: root refused: "); ok {
			st.Protection.Refusal = kind
		}
	}
	roots, rootsErr := gateProtectedRoots()
	sandbox := rootsErr == nil && claudeSandboxActive(root) && workerDenyReadCovers(roots)
	return telemetryDoctor(st, modelContext(cmd), sandbox, time.Now())
}

// claudeSandboxActive reports whether the person's Claude settings turn the
// sandbox on for this project: user, project and local settings in that
// order, the last that says either way winning.
func claudeSandboxActive(root string) bool {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".claude")
	}
	enabled := false
	for _, path := range []string{filepath.Join(dir, "settings.json"), filepath.Join(root, ".claude", "settings.json"), filepath.Join(root, ".claude", "settings.local.json")} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var s struct {
			Sandbox struct {
				Enabled *bool `json:"enabled"`
			} `json:"sandbox"`
		}
		if json.Unmarshal(data, &s) == nil && s.Sandbox.Enabled != nil {
			enabled = *s.Sandbox.Enabled
		}
	}
	return enabled
}

// workerDenyReadCovers verifies that the settings a Claude worker starts with
// list both store roots in denyRead.
func workerDenyReadCovers(roots []string) bool {
	var s struct {
		Sandbox struct {
			Filesystem struct {
				DenyRead []string `json:"denyRead"`
			} `json:"filesystem"`
		} `json:"sandbox"`
	}
	if json.Unmarshal([]byte(workerenv.ClaudeSettingsWithRoots("", false, nil, roots)), &s) != nil {
		return false
	}
	for _, root := range roots {
		if !slices.Contains(s.Sandbox.Filesystem.DenyRead, path.Clean(pathutil.Slashes(root))) {
			return false
		}
	}
	return len(roots) > 0
}

// doctorPanes joins the live Rex inventory to persisted session ownership. Only
// the pane recorded in Entry.Block is used; entry IDs, thread IDs and unrelated
// Rex blocks are ignored.
func doctorPanes(ctx context.Context, bin, rexSession string, reg *session.Registry) ([]doctor.Pane, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := []string{"block", "ls"}
	if rexSession != "" {
		args = append(args, "-s", rexSession)
	}
	cmd := exec.CommandContext(ctx, bin, append(args, "--json")...)
	cmd.Stdin = strings.NewReader("")
	data, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var inventory struct {
		Blocks []struct {
			ID       string `json:"block_id"`
			LegacyID string `json:"id"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(data, &inventory); err != nil {
		return nil, err
	}
	if inventory.Blocks == nil {
		return nil, fmt.Errorf("missing blocks list")
	}
	entries, err := reg.Entries()
	if err != nil {
		return nil, err
	}
	owners := make(map[string]string)
	for _, entry := range entries {
		if entry.Lead != "" && entry.Block != "" {
			owners[entry.Block] = entry.Lead
		}
	}
	var panes []doctor.Pane
	for _, block := range inventory.Blocks {
		if block.ID == "" {
			block.ID = block.LegacyID
		}
		if lead := owners[block.ID]; block.ID != "" && lead != "" {
			panes = append(panes, doctor.Pane{ID: block.ID, Lead: lead})
		}
	}
	return panes, nil
}
