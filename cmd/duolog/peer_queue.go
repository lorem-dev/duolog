package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/lorem-dev/duolog/internal/app/peer"
	"github.com/spf13/cobra"
)

// startQueueRun starts a detached queue run; tests replace it.
var startQueueRun = peer.StartQueueRun

// newPeerQueueCommand keeps the lead's ready queue: peers to open once
// admission allows.
func newPeerQueueCommand(d *cmdDeps, lead *string) *cobra.Command {
	queue := &cobra.Command{Use: "queue", Short: "Queue peers that open by themselves once a session is free"}
	run := func(cmd *cobra.Command, call func(ctx context.Context) error) error {
		ctx, cancel, err := peerContext(d, cmd, *lead, false, 0)
		if err != nil {
			return err
		}
		defer cancel()
		return call(ctx)
	}
	var cwd, taskPath, after string
	add := &cobra.Command{Use: "add <role> --task <file>", Short: "Queue a peer for a role, in a folder, with a task file", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd, func(ctx context.Context) error {
			item, err := peer.QueueAdd(ctx, args[0], cwd, taskPath, after)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), item.ID+" "+item.StateLine())
			return err
		})
	}}
	add.Flags().StringVar(&cwd, "cwd", ".", "Folder the peer works in: the project or one of its linked worktrees")
	add.Flags().StringVar(&taskPath, "task", "", "Task file, relative to --cwd")
	add.Flags().StringVar(&after, "after", "", "Wait until this peer is idle or closed")
	_ = add.MarkFlagRequired("task")
	list := &cobra.Command{Use: "list", Short: "List the queue in order: id, role, folder, task and state", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return run(cmd, func(ctx context.Context) error { return writeQueue(cmd, ctx) })
	}}
	remove := &cobra.Command{Use: "remove <id>", Short: "Drop an item from the queue", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return run(cmd, func(ctx context.Context) error {
			if err := peer.QueueRemove(ctx, args[0]); err != nil {
				return err
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), args[0]+" removed")
			return err
		})
	}}
	runCmd := &cobra.Command{Use: "run", Short: "Open queued peers in order while admission allows, then list the queue", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return run(cmd, func(ctx context.Context) error {
			err := peer.RunQueue(ctx)
			if errors.Is(err, peer.ErrQueueBusy) {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "a queue run is already going")
				return err
			}
			return errors.Join(err, writeQueue(cmd, ctx))
		})
	}}
	queue.AddCommand(add, list, remove, runCmd)
	return queue
}

// writeQueue prints one line per item: id, role, folder, task and state.
func writeQueue(cmd *cobra.Command, ctx context.Context) error {
	items, err := peer.QueueList(ctx)
	if err != nil {
		return err
	}
	root, err := projectRoot()
	if err != nil {
		return err
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	for _, item := range items {
		line := fmt.Sprintf("%s %s %s %s %s", item.ID, item.Role, peer.QueueRelative(root, item.Cwd), item.Task, item.StateLine())
		if item.After != "" && item.State == peer.QueueQueued {
			line += " after " + item.After
		}
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), line); err != nil {
			return err
		}
	}
	return nil
}
