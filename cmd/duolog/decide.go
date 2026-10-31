package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/spf13/cobra"
)

func newDecideCommand(kind string) *cobra.Command {
	var leadFlag string
	cmd := &cobra.Command{Use: kind + " <n>", Short: "Answer a worker permission request", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if isWorker() {
			return fmt.Errorf("workers cannot answer permission requests")
		}
		n, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil || n < 1 {
			return fmt.Errorf("invalid request number %q", args[0])
		}
		b, err := openBus()
		if err != nil {
			return err
		}
		reg, err := session.Open(b.Directory())
		if err != nil {
			return err
		}
		request, err := permissionRequest(b.Directory(), n)
		if err != nil {
			return err
		}
		if request.K != gate.Ask || request.From == "" {
			return fmt.Errorf("request %d is not a permission request", n)
		}
		// A late answer changes nothing, so saying it worked would mislead the lead.
		if gate.AskExpired(request.T) {
			return fmt.Errorf("request %d has expired: the worker was already denied", n)
		}
		if answered, err := alreadyAnswered(b.Directory(), n); err != nil {
			return err
		} else if answered != "" {
			return fmt.Errorf("request %d was already answered: %s", n, answered)
		}
		lead, ok, err := reg.LeadByAddress(request.To)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("lead is unavailable or has ended")
		}
		address := session.Address(lead)
		caller, err := reg.ResolveLead(leadFlag)
		if err != nil {
			return err
		}
		if caller.ID != lead.ID {
			return fmt.Errorf("request %d does not belong to lead %q", n, caller.ID)
		}
		_, err = b.Send(bus.Message{From: address, To: request.From, K: kind, Ref: strconv.FormatInt(n, 10), S: kind})
		return err
	}}
	cmd.Flags().StringVar(&leadFlag, "lead", "", "Optional check: lead session ID or bus address that must own the request")
	return cmd
}

// alreadyAnswered returns the first decision recorded for a request.
func alreadyAnswered(dir string, n int64) (string, error) {
	f, err := os.Open(filepath.Join(dir, "bus.ndjson"))
	if err != nil {
		return "", err
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	ref := strconv.FormatInt(n, 10)
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		var m bus.Message
		if json.Unmarshal(line, &m) == nil && m.Ref == ref && (m.K == gate.Allow || m.K == gate.Deny) {
			return m.K, nil
		}
	}
}

// Read the request without advancing the lead's delivery cursor. Ignore a
// concurrent incomplete tail, just as Inbox does.
func permissionRequest(dir string, n int64) (bus.Message, error) {
	f, err := os.Open(filepath.Join(dir, "bus.ndjson"))
	if err != nil {
		return bus.Message{}, err
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			return bus.Message{}, err
		}
		var m bus.Message
		if err := json.Unmarshal(line, &m); err != nil {
			return bus.Message{}, err
		}
		if m.N == n {
			return m, nil
		}
	}
	return bus.Message{}, fmt.Errorf("unknown permission request %d", n)
}
