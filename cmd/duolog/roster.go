package main

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/roster"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/wiring"
	"github.com/spf13/cobra"
)

// printCapsNotes says what each registered agent but the lead's lacks, and
// whether it has duolog hooks.
func printCapsNotes(w io.Writer, leadAgent, root string) error {
	agents := wiring.RosterCaps()
	mine := roster.Cached(agents, leadAgent, root)
	for _, other := range wiring.Names() {
		if other == leadAgent {
			continue
		}
		theirs := roster.Cached(agents, other, root)
		if note := roster.CapsNote(mine, theirs, roster.HasDuolog(agents, other, root)); note != "" {
			if _, err := fmt.Fprintln(w, note); err != nil {
				return err
			}
		}
	}
	return nil
}

// printVersions prints what each model alias resolves to, or where its price
// reference is.
func printVersions(cmd *cobra.Command, d *cmdDeps, cfg *config.Config, cacheDir string) error {
	aliases := make([]string, 0, len(cfg.Models))
	for alias := range cfg.Models {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		model := cfg.Models[alias]
		var detail string
		if model.Agent == "codex" {
			resolved, err := d.Delegate.Agents[model.Agent].Models.Resolve(cmd.Context(), cfg, cacheDir, config.Target{Agent: model.Agent, Model: alias})
			if err != nil {
				detail = "unavailable: " + err.Error()
			} else {
				detail = resolved
			}
		} else {
			// Claude resolves aliases only when it runs. A price reference is not
			// evidence of the concrete version that a future invocation will select.
			detail = "resolved by Claude at run time; price reference=" + model.PricedFor
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", alias, detail); err != nil {
			return err
		}
	}
	return nil
}

func newRosterCommand(d *cmdDeps) *cobra.Command {
	var versions bool
	var leadFlag string
	cmd := &cobra.Command{Use: "roster", Short: "Show role model aliases", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := projectRoot()
		if err != nil {
			return err
		}
		cfg, err := configfile.Load(root, wiring.ConfigOptions())
		if err != nil {
			return err
		}
		reg, err := session.Open(project.LeadHome(root))
		if err != nil {
			return err
		}
		lead, err := reg.ResolveLead(leadFlag)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), roster.Line(cfg, wiring.Names(), lead.ID, reg)); err != nil {
			return err
		}
		if leadSession, ok, _ := reg.Lead(lead.ID); ok {
			if err := printCapsNotes(cmd.OutOrStdout(), leadSession.Agent, root); err != nil {
				return err
			}
		}
		if !versions {
			return nil
		}
		return printVersions(cmd, d, cfg, filepath.Join(root, ".duolog", "cache"))
	}}
	cmd.Flags().StringVar(&leadFlag, "lead", "", "Registered lead session ID")
	cmd.Flags().BoolVar(&versions, "versions", false, "Include resolved Codex versions and Claude price references")
	return cmd
}
