package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/lorem-dev/duolog/internal/coauthor"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/spf13/cobra"
)

func newCoauthorsCommand() *cobra.Command {
	var since, hook string
	cmd := &cobra.Command{Use: "coauthors", Short: "Print commit co-author trailers for workers", Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, args []string) (err error) {
		if hook != "" {
			defer func() { err = nil }() // A hook must never block a commit.
			if isWorker() {
				return nil
			}
		}
		root, err := projectRoot()
		if err != nil {
			return err
		}
		cfg, err := config.Load(root)
		if err != nil {
			return err
		}
		if !cfg.Coauthors.Enabled {
			return nil
		}
		dir := filepath.Join(root, ".duolog")
		all, err := coauthor.Lines(dir, cfg.Coauthors, time.Time{})
		if err != nil || len(all) == 0 {
			return err
		}
		cutoff, err := coauthor.Since(workRoot(root), since)
		if err != nil {
			return err
		}
		lines, err := coauthor.Lines(dir, cfg.Coauthors, cutoff)
		if err != nil {
			return err
		}
		if hook != "" {
			return coauthor.Hook(hook, lines)
		}
		for _, line := range lines {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), line); err != nil {
				return err
			}
		}
		return nil
	}
	cmd.Flags().StringVar(&since, "since", "HEAD", "Only credit changes recorded after this revision's committer time")
	cmd.Flags().StringVar(&hook, "hook", "", "Append missing trailers to a Git commit message; always succeed")
	return cmd
}
