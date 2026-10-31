package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/doctor"
	"github.com/lorem-dev/duolog/internal/rex"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/spf13/cobra"
)

func newDoctorCommand() *cobra.Command {
	var probe bool
	cmd := &cobra.Command{Use: "doctor", Short: "Report agent health, trust and availability", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := projectRoot()
		if err != nil {
			return err
		}
		cfg, err := config.Load(root)
		if err != nil {
			return err
		}
		reg, err := session.Open(filepath.Join(root, ".duolog"))
		if err != nil {
			return err
		}
		opts := doctor.Options{Root: root, Registry: reg, Probe: probe}
		var inventoryFindings []doctor.Finding
		if bin, ok := rex.Locate(os.Getenv("REX_BIN_DIR"), rex.BundleGlobs()); ok {
			opts.RexBin = bin
			opts.Panes, err = doctorPanes(cmd.Context(), bin, os.Getenv("REX_SESSION"), reg)
			if err != nil {
				inventoryFindings = append(inventoryFindings, doctor.Finding{Level: "warning", Message: "Rex pane inventory unavailable: " + err.Error()})
			} else {
				inventoryFindings = append(inventoryFindings, doctor.Finding{Level: "info", Message: "Rex pane inventory checked"})
			}
		}
		for _, finding := range append(inventoryFindings, doctor.Run(cmd.Context(), cfg, opts)...) {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", finding.Level, finding.Message); err != nil {
				return err
			}
		}
		return nil
	}}
	cmd.Flags().BoolVar(&probe, "probe", false, "Make a minimal model call from an empty temporary folder")
	return cmd
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
