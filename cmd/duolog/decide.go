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
	return &cobra.Command{Use: kind + " <n>", Short: "Answer a worker permission request", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
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
		lead, ok, err := reg.Lead(session.CurrentLead(os.Environ()))
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("lead is unavailable or has ended")
		}
		address := session.Address(lead)
		request, err := permissionRequest(b.Directory(), n)
		if err != nil {
			return err
		}
		if request.K != gate.Ask || request.To != address || request.From == "" {
			return fmt.Errorf("request %d does not belong to this lead", n)
		}
		_, err = b.Send(bus.Message{From: address, To: request.From, K: kind, Ref: strconv.FormatInt(n, 10), S: kind})
		return err
	}}
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
