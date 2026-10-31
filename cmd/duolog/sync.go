package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/lorem-dev/duolog/internal/agentsync"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/trust"
	"github.com/spf13/cobra"
)

func newSyncCommand() *cobra.Command {
	var o agentsync.Options
	var modelFlags []string
	cmd := &cobra.Command{Use: "sync", Short: "Generate agent and skill files for the other agent", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if o.AgentsOnly && o.SkillsOnly {
			return errors.New("--agents-only and --skills-only exclude each other")
		}
		if len(modelFlags) > 0 {
			o.Models = map[string]string{}
		}
		for _, f := range modelFlags {
			from, to, ok := strings.Cut(f, "=")
			if !ok || from == "" || to == "" {
				return fmt.Errorf("--model wants source=target, for example opus=terra, got %q", f)
			}
			o.Models[from] = to
		}
		root, err := projectRoot()
		if err != nil {
			return err
		}
		cfg, err := config.Load(root)
		if err != nil {
			return err
		}
		if !o.DryRun && !o.Check {
			if _, err := store.Open(root); err != nil {
				return err
			}
		}
		rep, err := agentsync.Run(root, cfg.Sync, o)
		if err != nil {
			return err
		}
		out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
		for _, p := range rep.Written {
			fmt.Fprintln(out, "write  "+p)
		}
		for _, p := range rep.Removed {
			fmt.Fprintln(out, "remove "+p)
		}
		for _, w := range rep.Warnings {
			fmt.Fprintln(errOut, "warning: "+w)
		}
		for _, c := range rep.Conflicts {
			fmt.Fprintln(errOut, "refused: "+c)
		}
		if !o.DryRun && !o.Check && wroteCodexAgent(rep.Written) {
			if ok, err := trust.Folder("codex", root); err == nil && !ok {
				fmt.Fprintln(errOut, "note: Codex loads project agent files only in a trusted folder; trust this folder in Codex to use them.")
			}
		}
		switch {
		case len(rep.Conflicts) > 0:
			return fmt.Errorf("%d file(s) refused", len(rep.Conflicts))
		case o.Check && len(rep.Written)+len(rep.Removed) > 0:
			return fmt.Errorf("generated files are out of date: %d to write, %d to remove", len(rep.Written), len(rep.Removed))
		}
		return nil
	}}
	f := cmd.Flags()
	f.StringVar(&o.From, "from", "", "Side that holds the sources, claude or codex; needed on the first run")
	f.BoolVar(&o.AgentsOnly, "agents-only", false, "Sync agent files only")
	f.BoolVar(&o.SkillsOnly, "skills-only", false, "Sync skills only")
	f.BoolVar(&o.DryRun, "dry-run", false, "Show what would change and write nothing")
	f.BoolVar(&o.Check, "check", false, "Write nothing and exit 1 if a generated file is out of date or refused")
	f.BoolVar(&o.Force, "force", false, "Overwrite generated files that were edited by hand")
	f.BoolVar(&o.SwitchDirection, "switch-direction", false, "Allow --from to reverse the saved direction; the old sources are rebuilt")
	f.StringArrayVar(&modelFlags, "model", nil, "Map a source model alias to a target alias, for example opus=terra")
	f.StringVar(&o.Path, "path", "", "Sync only the agent file or skill that holds this path")
	return cmd
}

func wroteCodexAgent(written []string) bool {
	for _, p := range written {
		if strings.HasPrefix(p, ".codex/agents/") {
			return true
		}
	}
	return false
}
