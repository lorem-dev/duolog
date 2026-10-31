package main

import (
	"fmt"
	"strings"

	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/spf13/cobra"
)

func openBus() (*bus.Bus, error) {
	root, err := projectRoot()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	s, err := store.Open(root)
	if err != nil {
		return nil, err
	}
	return bus.Open(s.Dir, cfg.Wire)
}

func newSendCommand() *cobra.Command {
	var m bus.Message
	cmd := &cobra.Command{
		Use:   "send <summary>",
		Short: "Send a message on the project bus",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if m.K == "allow" || m.K == "deny" {
				return fmt.Errorf("kind %q is reserved for duolog %s", m.K, m.K)
			}
			if isWorker() {
				if _, name, ok := strings.Cut(m.From, "/"); ok && strings.HasPrefix(name, "lead.") {
					return fmt.Errorf("workers cannot send as a lead address")
				}
			}
			b, err := openBus()
			if err != nil {
				return err
			}
			m.S = args[0]
			n, err := b.Send(m)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), n)
			return err
		},
	}
	cmd.Flags().StringVar(&m.From, "from", "", "Sender address")
	cmd.Flags().StringVar(&m.To, "to", "", "Recipient address or *")
	cmd.Flags().StringVar(&m.K, "kind", "", "Message kind")
	cmd.Flags().StringVar(&m.Th, "thread", "", "Thread identifier")
	cmd.Flags().StringVar(&m.Ref, "ref", "", "Artifact reference")
	for _, name := range []string{"from", "to", "kind"} {
		_ = cmd.MarkFlagRequired(name)
	}
	return cmd
}

func newInboxCommand() *cobra.Command {
	var reader string
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "Print new messages addressed to a reader",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := openBus()
			if err != nil {
				return err
			}
			lines, err := b.Inbox(reader)
			if err != nil {
				return err
			}
			for _, line := range lines {
				if _, err := cmd.OutOrStdout().Write(line); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&reader, "reader", "", "Reader address")
	_ = cmd.MarkFlagRequired("reader")
	return cmd
}
