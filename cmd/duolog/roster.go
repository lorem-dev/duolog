package main

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/lorem-dev/duolog/internal/capability"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/models"
	"github.com/lorem-dev/duolog/internal/roster"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/spf13/cobra"
)

func newRosterCommand() *cobra.Command {
	var versions bool
	var leadFlag string
	cmd := &cobra.Command{Use: "roster", Short: "Show role model aliases", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
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
		lead, err := reg.ResolveLead(leadFlag)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), roster.Line(cfg, lead.ID, reg)); err != nil {
			return err
		}
		if leadSession, ok, _ := reg.Lead(lead.ID); ok {
			other := "codex"
			if leadSession.Agent == "codex" {
				other = "claude"
			}
			mine := capability.Cached(leadSession.Agent, root)
			theirs := capability.Cached(other, root)
			if note := roster.CapsNote(mine, theirs, capability.HasDuolog(other, root)); note != "" {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), note); err != nil {
					return err
				}
			}
		}
		if !versions {
			return nil
		}
		aliases := make([]string, 0, len(cfg.Models))
		for alias := range cfg.Models {
			aliases = append(aliases, alias)
		}
		sort.Strings(aliases)
		for _, alias := range aliases {
			model := cfg.Models[alias]
			var detail string
			if model.Agent == "codex" {
				resolved, err := models.Resolve(cmd.Context(), cfg, filepath.Join(reg.Dir, "cache"), config.Target{Agent: model.Agent, Model: alias})
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
	}}
	cmd.Flags().StringVar(&leadFlag, "lead", "", "Registered lead session ID")
	cmd.Flags().BoolVar(&versions, "versions", false, "Include resolved Codex versions and Claude price references")
	return cmd
}
