package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/app/doctor"
	"github.com/lorem-dev/duolog/internal/app/install"
	"github.com/lorem-dev/duolog/internal/project"
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
// that the config sets, and the names the Codex MCP server table forwards
// (wiring.ServerEnvNames). User scope has no skills folder. When the config
// does not load, it returns the plain deps and the error. Only a release
// version permits automatic marketplace plugin installation.
func configuredInstall(d *cmdDeps, o install.Options) (install.Deps, error) {
	deps := wiring.WithServerEnv(d.Install, wiring.ServerEnvNames(o.Root))
	deps.Release = releaseVersion.MatchString(version)
	if o.Scope == "user" {
		return deps, nil
	}
	dirs, err := configfile.ProjectSkillsDirs(o.Root, wiring.Descriptors())
	if err != nil {
		return d.Install, err
	}
	return wiring.ConfiguredInstall(deps, dirs), nil
}

// releaseVersion matches a release version without a checkout or dev suffix.
var releaseVersion = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`)

// newInstallCommand installs the selected agents or checks their state
// without starting marketplace commands or writing files.
func newInstallCommand(d *cmdDeps) *cobra.Command {
	var scope, agent string
	var check, hooksOnly, statusLine, noPlugin bool
	cmd := &cobra.Command{Use: "install", Short: "Install duolog plugins, hooks and skills for Claude Code and Codex", Args: cobra.NoArgs, PreRunE: refuseWorkerCommand, RunE: func(cmd *cobra.Command, args []string) error {
		if hooksOnly && !check {
			return errors.New("--hooks-only works only with --check")
		}
		o, err := installOptions(scope, agent)
		if err != nil {
			return err
		}
		o.NoPlugin = noPlugin
		deps, err := configuredInstall(d, o)
		if err != nil {
			return err
		}
		if check {
			o.HooksOnly = hooksOnly
			return installCheck(cmd, deps, o)
		}
		o.StatusLine = statusLine
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
		if o.Scope != "user" {
			printInstallTrustNote(cmd, d, o.Root)
		}
		return nil
	}}
	cmd.Flags().StringVar(&scope, "scope", "project", "Where to install: project or user")
	cmd.Flags().StringVar(&agent, "agent", "both", agentFlagHelp("both"))
	cmd.Flags().BoolVar(&check, "check", false, "Write nothing; exit 1 when install would change the installed hooks or skills, or they cannot be checked")
	cmd.Flags().BoolVar(&hooksOnly, "hooks-only", false, "With --check: compare duolog's hooks and leave the skills out")
	cmd.Flags().BoolVar(&statusLine, "status-line", false, "Add duolog's Claude status line again after you removed it")
	cmd.Flags().BoolVar(&noPlugin, "no-plugin", false, "Skip agent plugin installation and missing-plugin notes")
	return cmd
}

// printInstallTrustNote prints, after a project install, what Codex still
// needs before duolog can start a Codex worker in this checkout.
func printInstallTrustNote(cmd *cobra.Command, d *cmdDeps, root string) {
	cfg, err := loadConfig(root)
	if err != nil {
		fmt.Fprintln(cmd.OutOrStdout(), "Codex trust could not be checked: "+err.Error())
		return
	}
	trustOf := func(agent string) doctor.FolderTrust {
		if t := d.Delegate.Agents[agent].Trust; t != nil {
			return t
		}
		return nil
	}
	if note := doctor.InstallTrustNote(cfg, trustOf, project.CurrentWorkRoot(root)); note != "" {
		fmt.Fprintln(cmd.OutOrStdout(), note)
	}
}

// installCheck prints the lines of each selected agent's install at o's
// scope. It fails when install would change one, or one could not be
// checked; a line for the person alone, such as a file to remove, does not
// fail it. It writes nothing.
func installCheck(cmd *cobra.Command, d install.Deps, o install.Options) error {
	statuses, err := d.Check(o)
	if err != nil {
		return err
	}
	stale, broken := false, false
	for _, s := range statuses {
		lines := s.Lines(o.Root)
		stale, broken = stale || s.Stale(), broken || s.Err != nil
		if s.Err == nil && !s.Stale() {
			lines = append(lines, settledLine(s))
		}
		if line := s.LeadLine(o.Root); line != "" {
			lines = append(lines, line)
		}
		if line := s.StatusLineLine(o.Root); line != "" {
			lines = append(lines, line)
		}
		for _, line := range lines {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), line); err != nil {
				return err
			}
		}
	}
	if o.Scope != "user" {
		if err := printBlockedProjectChecks(cmd.Context(), cmd.OutOrStdout(), o.Root); err != nil {
			return err
		}
	}
	switch {
	case broken:
		return errors.New("the duolog install could not be checked")
	case stale:
		return errors.New("the duolog install is out of date")
	}
	return nil
}

// settledLine says what install --check found for an install that install
// would not change.
func settledLine(s install.Status) string {
	switch {
	case !s.Installed:
		return s.Agent + " " + s.Scope + " install: not installed"
	case s.Dev:
		return s.Agent + " " + s.Scope + " install runs the dev launcher of this checkout; not compared"
	}
	return s.Agent + " " + s.Scope + " install is current"
}

func newUninstallCommand(d *cmdDeps) *cobra.Command {
	var scope, agent string
	cmd := &cobra.Command{Use: "uninstall", Short: "Remove duolog hooks and skill, keeping other hooks and the config", Args: cobra.NoArgs, PreRunE: refuseWorkerCommand, RunE: func(cmd *cobra.Command, args []string) error {
		o, err := installOptions(scope, agent)
		if err != nil {
			return err
		}
		// A config that does not load must not block an uninstall: the default
		// folder is cleaned, and a custom folder stays for the person to remove.
		deps, _ := configuredInstall(d, o)
		report, err := deps.Uninstall(o)
		for _, n := range report.Notes {
			fmt.Fprintln(cmd.OutOrStdout(), n)
		}
		return err
	}}
	cmd.Flags().StringVar(&scope, "scope", "project", "Where to uninstall: project or user")
	cmd.Flags().StringVar(&agent, "agent", "both", agentFlagHelp("both"))
	return cmd
}
