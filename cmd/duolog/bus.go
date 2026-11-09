package main

import (
	"fmt"
	"strings"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/wiring"
	"github.com/spf13/cobra"
)

func openBus() (*bus.Bus, error) {
	b, _, err := openBusWire()
	return b, err
}

// openBusWire is openBus with the wire limits it was opened under.
func openBusWire() (*bus.Bus, config.Wire, error) {
	root, err := projectRoot()
	if err != nil {
		return nil, config.Wire{}, err
	}
	cfg, err := configfile.Load(root, wiring.ConfigOptions())
	if err != nil {
		return nil, config.Wire{}, err
	}
	s, err := store.Open(root)
	if err != nil {
		return nil, config.Wire{}, err
	}
	b, err := bus.Open(s.Dir, cfg.Wire)
	return b, cfg.Wire, err
}

// redirectToWorkersLead applies the rule for a message to a worker or peer:
// from anyone but its own lead it goes to that lead, with a note of whom it was
// meant for and who sent it, because nobody watches a worker's own inbox. The
// recipient is a live worker or peer, by its id or its agent/role address. A
// message from the lead, one to any other recipient and one for a worker whose
// lead is gone are as sent. An address that names workers of more than one
// lead is an error, and nothing is sent. The second result is the lead's address when the
// message moved, "" otherwise.
func redirectToWorkersLead(reg *session.Registry, m bus.Message, wire config.Wire) (bus.Message, string, error) {
	entries, err := reg.Entries()
	if err != nil {
		return m, "", err
	}
	var target string
	for _, e := range entries {
		if e.Closed || e.Lead == "" || m.To != e.ID && m.To != e.Agent+"/"+e.Role {
			continue
		}
		lead, ok, err := reg.Lead(e.Lead)
		if err != nil {
			return m, "", err
		}
		if !ok {
			continue
		}
		address := session.Address(lead)
		if target != "" && address != target {
			return m, "", fmt.Errorf("%s names workers of more than one lead (%s and %s); send to a worker by its id, or to its lead; nothing was sent", m.To, target, address)
		}
		target = address
	}
	if target == "" || target == m.From {
		return m, "", nil
	}
	note := "for " + m.To + " from " + m.From + ": " + m.S
	if len(note) > wire.MaxSummaryBytes {
		return m, "", bus.ErrTooLong
	}
	m.To, m.S = target, note
	return m, target, nil
}

// deliver sends m on the project bus, to the lead of the worker it names when
// that is not its own lead's message, and prints its number.
func deliver(cmd *cobra.Command, m bus.Message) error {
	b, wire, err := openBusWire()
	if err != nil {
		return err
	}
	reg, err := session.Open(b.Directory())
	if err != nil {
		return err
	}
	sent, lead, err := redirectToWorkersLead(reg, m, wire)
	if err != nil {
		return err
	}
	n, err := b.Send(sent)
	if err != nil {
		return err
	}
	if lead != "" {
		if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "%s belongs to the lead %s: the message went to that lead\n", m.To, lead); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), n)
	return err
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
			m.S = args[0]
			return deliver(cmd, m)
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
			if reader == "" {
				// A lead's hooks name no reader: its own address is the default.
				reg, err := session.Open(b.Directory())
				if err != nil {
					return err
				}
				lead, err := reg.ResolveLead("")
				if err != nil {
					return fmt.Errorf("no --reader given and no lead to read for: %w", err)
				}
				reader = session.Address(lead)
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
	cmd.Flags().StringVar(&reader, "reader", "", "Reader address; the current lead when omitted")
	return cmd
}
