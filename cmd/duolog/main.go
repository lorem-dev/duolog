package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "duolog",
		Short:        "Coordinate Claude Code and Codex tasks",
		SilenceUsage: true,
	}
	cmd.AddCommand(newConfigCommand(), newRouteCommand(), newSendCommand(), newInboxCommand())
	return cmd
}

// projectRoot uses the common Git directory so linked worktrees share settings.
// A directory outside a Git repository uses its working directory as the root.
func projectRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	cmd := exec.Command("git", "rev-parse", "--git-common-dir")
	cmd.Dir = cwd
	output, err := cmd.Output()
	if err != nil {
		return cwd, nil
	}
	common := strings.TrimSpace(string(output))
	if !filepath.IsAbs(common) {
		common = filepath.Join(cwd, common)
	}
	return filepath.Dir(filepath.Clean(common)), nil
}

func main() {
	if err := newRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}
