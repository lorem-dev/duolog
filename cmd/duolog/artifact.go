package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/lorem-dev/duolog/internal/artifact"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/spf13/cobra"
)

func newPutCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "put <path>",
		Short: "Save an immutable artifact snapshot",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := projectRoot()
			if err != nil {
				return err
			}
			s, err := store.Open(root)
			if err != nil {
				return err
			}
			ref, err := artifact.Put(s, args[0])
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), ref)
			return err
		},
	}
}

func newRefCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "ref <path[:start-end]>",
		Short: "Create a checked repository file reference",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := repositoryRoot()
			if err != nil {
				return err
			}
			ref, err := artifact.RepoRef(root, args[0])
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), ref)
			return err
		},
	}
}

func newReadCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "read <ref>",
		Short: "Read an artifact or repository file reference",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := projectRoot()
			if err != nil {
				return err
			}
			s, err := store.Open(root)
			if err != nil {
				return err
			}
			repoRoot, err := repositoryRoot()
			if err != nil {
				return err
			}
			content, stale, err := artifact.Read(s, repoRoot, args[0])
			if err != nil {
				return err
			}
			if stale {
				if _, err := fmt.Fprintln(cmd.ErrOrStderr(), "warning: stale ref; returning current content"); err != nil {
					return err
				}
			}
			_, err = cmd.OutOrStdout().Write(content)
			return err
		},
	}
}

// repositoryRoot resolves file references in the active worktree. The shared
// settings and snapshot store continue to use projectRoot.
func repositoryRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = cwd
	output, err := cmd.Output()
	if err != nil {
		return cwd, nil
	}
	return strings.TrimSpace(string(output)), nil
}
