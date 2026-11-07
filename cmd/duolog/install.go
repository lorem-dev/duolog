package main

import (
	"fmt"
	"os"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/app/install"
	"github.com/lorem-dev/duolog/internal/wiring"
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

// configuredInstall returns the install deps with the project skills folder
// that the config sets. User scope has none. When the config does not load, it
// returns the plain deps and the error.
func configuredInstall(d *cmdDeps, o install.Options) (install.Deps, error) {
	if o.Scope == "user" {
		return d.Install, nil
	}
	dirs, err := configfile.ProjectSkillsDirs(o.Root, wiring.Descriptors())
	if err != nil {
		return d.Install, err
	}
	return wiring.ConfiguredInstall(d.Install, dirs), nil
}

func newInstallCommand(d *cmdDeps) *cobra.Command {
	var scope, agent string
	cmd := &cobra.Command{Use: "install", Short: "Install duolog hooks and skill for Claude Code and Codex", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		o, err := installOptions(scope, agent)
		if err != nil {
			return err
		}
		deps, err := configuredInstall(d, o)
		if err != nil {
			return err
		}
		report, err := deps.Run(o)
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
	cmd.Flags().StringVar(&agent, "agent", "both", agentFlagHelp("both"))
	return cmd
}

func newUninstallCommand(d *cmdDeps) *cobra.Command {
	var scope, agent string
	cmd := &cobra.Command{Use: "uninstall", Short: "Remove duolog hooks and skill, keeping other hooks and the config", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		o, err := installOptions(scope, agent)
		if err != nil {
			return err
		}
		// A config that does not load must not block an uninstall: the default
		// folder is cleaned, and a custom folder stays for the person to remove.
		deps, _ := configuredInstall(d, o)
		return deps.Remove(o)
	}}
	cmd.Flags().StringVar(&scope, "scope", "project", "Where to uninstall: project or user")
	cmd.Flags().StringVar(&agent, "agent", "both", agentFlagHelp("both"))
	return cmd
}
