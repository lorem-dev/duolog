package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/spf13/cobra"
)

func newDecideCommand(kind string, d *cmdDeps) *cobra.Command {
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
		request, err := openRequest(d, reg, b.Directory(), n)
		if err != nil {
			return err
		}
		lead, ok, err := reg.LeadByAddress(request.To)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("lead is unavailable or has ended")
		}
		if err := checkCaller(d, reg, lead, n, leadFlag); err != nil {
			return err
		}
		_, err = b.Send(bus.Message{From: session.Address(lead), To: request.From, K: kind, Ref: strconv.FormatInt(n, 10), S: kind})
		return err
	}}
	cmd.Flags().StringVar(&leadFlag, "lead", "", "Optional check: lead session ID or bus address that must own the request")
	return cmd
}

// openRequest returns permission request n from the bus in dir when it may
// still be answered by this caller: an ask that has neither expired nor been
// answered, and, when it is person-only, a caller that checkPerson lets in.
func openRequest(d *cmdDeps, reg *session.Registry, dir string, n int64) (bus.Message, error) {
	request, err := permissionRequest(dir, n)
	if err != nil {
		return bus.Message{}, err
	}
	if request.K != gate.Ask || request.From == "" {
		return bus.Message{}, fmt.Errorf("request %d is not a permission request", n)
	}
	if request.P {
		if err := checkPerson(d, reg, n); err != nil {
			return bus.Message{}, err
		}
	}
	// A late answer changes nothing, so saying it worked would mislead the lead.
	if gate.AskExpired(request) {
		return bus.Message{}, fmt.Errorf("request %d has expired: the worker was already denied", n)
	}
	if answered, err := alreadyAnswered(dir, n); err != nil {
		return bus.Message{}, err
	} else if answered != "" {
		return bus.Message{}, fmt.Errorf("request %d was already answered: %s", n, answered)
	}
	return request, nil
}

// checkCaller refuses an answer to request n, owned by lead, from a caller
// that does not own it. The caller is the lead among this command's parent
// processes: --lead cannot change it. An agent session above it that is no
// lead of this project answers nothing here: it may be another project's
// lead that changed folder. An agent process between this command and the
// lead makes it a worker or peer, refused too. Only a caller with no agent
// above it, as the person's own terminal, goes on to --lead as before;
// leadFlag must agree in every case.
//
// This check is best effort, like the lead guard. It follows the live parent
// chain and CLAUDECODE, and every process runs as the same user: a process
// that detaches, as with nohup, setsid, tmux new -d or a Rex pane, is
// re-parented away from its lead, may drop CLAUDECODE, and then looks like
// the person's terminal. No check within one user tells them apart.
func checkCaller(d *cmdDeps, reg *session.Registry, lead session.Lead, n int64, leadFlag string) error {
	// Leads that share one process: the owner, or the one --lead names.
	above, found, err := reg.AncestorLead(lead.ID, leadFlag)
	switch {
	case err != nil:
		return err
	case found && above.ID != lead.ID:
		return fmt.Errorf("request %d belongs to another lead, not to lead %q, which runs this command; --lead does not change that", n, above.ID)
	case found && leadFlag == "" && session.CurrentLead(os.Environ()) == "":
		return nil
	case !found && d.AgentAbove(os.Environ()):
		return fmt.Errorf("request %d: this command runs in an agent session that is no lead of this project; the person answers it in their own terminal, or the lead runs duolog lead register in its own session", n)
	}
	caller, err := reg.ResolveLead(leadFlag)
	if err != nil {
		return err
	}
	if caller.ID != lead.ID {
		return fmt.Errorf("request %d does not belong to lead %q", n, caller.ID)
	}
	return nil
}

// checkPerson refuses an answer to person-only request n from a caller with
// an agent above it: a lead, a Claude subagent in the lead's own process, a
// worker or a peer. A subagent shares the lead's process, so no walk tells
// the two apart; only the person's own terminal answers such an ask. A lead
// of this project among the parent processes refuses, as does any Claude or
// Codex process or Claude's CLAUDECODE mark. It is best effort, as
// checkCaller is.
func checkPerson(d *cmdDeps, reg *session.Registry, n int64) error {
	refused := fmt.Errorf("request %d is the person's to answer: it reaches outside the work root, and no lead, subagent, worker or peer answers it; the person runs duolog allow or deny in their own terminal", n)
	if d.AgentAbove(os.Environ()) {
		return refused
	}
	// A walk error refuses too: it found a lead above through an agent
	// process or beside another lead of its process, or could not read the
	// registry.
	if _, found, err := reg.AncestorLead(); err != nil || found {
		return refused
	}
	return nil
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
