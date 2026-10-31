package main

import (
	"fmt"
	"os"

	"github.com/lorem-dev/duolog/internal/install"
	"github.com/spf13/cobra"
)

func installOptions(scope, agent string) (install.Options, error) {
	root, err := projectRoot()
	if err != nil {
		return install.Options{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return install.Options{}, err
	}
	return install.Options{Scope: scope, Agent: agent, Home: home, Root: root}, nil
}

func newInstallCommand() *cobra.Command {
	var scope, agent string
	cmd := &cobra.Command{Use: "install", Short: "Install duolog hooks and skill for Claude Code and Codex", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		o, err := installOptions(scope, agent)
		if err != nil {
			return err
		}
		report, err := install.Run(o)
		if err != nil {
			return err
		}
		for _, f := range report.Files {
			fmt.Fprintln(cmd.OutOrStdout(), "wrote", f)
		}
		if len(report.Files) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "already installed")
		}
		for _, n := range report.Notes {
			fmt.Fprintln(cmd.OutOrStdout(), n)
		}
		return nil
	}}
	cmd.Flags().StringVar(&scope, "scope", "project", "Where to install: project or user")
	cmd.Flags().StringVar(&agent, "agent", "both", "Which agent: claude, codex or both")
	return cmd
}

func newUninstallCommand() *cobra.Command {
	var scope, agent string
	cmd := &cobra.Command{Use: "uninstall", Short: "Remove duolog hooks and skill, keeping other hooks and the config", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		o, err := installOptions(scope, agent)
		if err != nil {
			return err
		}
		return install.Remove(o)
	}}
	cmd.Flags().StringVar(&scope, "scope", "project", "Where to uninstall: project or user")
	cmd.Flags().StringVar(&agent, "agent", "both", "Which agent: claude, codex or both")
	return cmd
}
