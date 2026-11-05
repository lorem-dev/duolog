package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/pathutil"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/session"
)

func leadGuard(stateDir, agent string, input []byte) (gate.Decision, bool) {
	actions, err := gate.NormalizeAll(agent, input)
	if err != nil {
		return gate.Decision{}, false
	}
	var envelope struct {
		Cwd   string `json:"cwd"`
		Input struct {
			Command json.RawMessage `json:"command"`
		} `json:"tool_input"`
	}
	if json.Unmarshal(input, &envelope) == nil {
		var command string
		if json.Unmarshal(envelope.Input.Command, &command) == nil && command != "" {
			hasExec := false
			for _, action := range actions {
				hasExec = hasExec || action.Kind == "exec"
			}
			if !hasExec {
				actions = append(actions, gate.Action{Kind: "exec", Target: command})
			}
		}
	}
	decision := func(number string, request bus.Message) gate.Decision {
		kind := gate.Ask
		reason := "this command may allow an ask that reads or writes outside the project; only the person may allow it"
		if number != "" {
			reason = fmt.Sprintf("ask %s reads or writes outside the project; only the person may allow it", number)
		}
		if agent == "codex" {
			kind = gate.Deny
			if number == "" {
				number = "N"
			}
			owner := request.To
			if owner != "" {
				if reg, err := session.Open(stateDir); err == nil {
					if lead, ok, err := reg.LeadByAddress(owner); err == nil && ok {
						owner = lead.ID
					}
				}
			}
			if owner == "" {
				owner = "<lead id>"
			}
			reason += fmt.Sprintf(": they run duolog allow %s --lead %s in their own terminal in %s", number, owner, filepath.Base(filepath.Dir(stateDir)))
		}
		return gate.Decision{Kind: kind, Reason: reason, Rule: "person-only ask"}
	}
	for _, action := range actions {
		if action.Kind != "exec" {
			continue
		}
		parts, uncertain := gate.ShellParts(action.Target, action.Shell)
		if uncertain {
			// ShellParts can omit wrappers such as env; inspect their tokens too.
			parts = append(parts, action.Target)
		}
		unknownNumber := false
		for _, part := range parts {
			if args, ok := leadCommandArgs(part, "remember"); ok {
				root := filepath.Dir(stateDir)
				cwd := envelope.Cwd
				if cwd == "" {
					cwd = root
				}
				work := project.WorkRoot(cwd, root)
				if gate.ExecOutside(gate.Action{Kind: "exec", Target: strings.Join(args, " "), Work: work, Cwd: cwd, Shell: action.Shell}) {
					d := gate.Decision{Kind: gate.Ask, Rule: "person-only ask", Reason: "this command may approve a rule outside the project; only the person may allow it"}
					if agent == "codex" {
						d.Kind = gate.Deny
						d.Reason += ": they run duolog remember with the approved rule in their own terminal in " + filepath.Base(root)
					}
					return d, true
				}
			}
			if n, ok := leadAllowRequest(part); ok {
				if n == 0 {
					unknownNumber = true
					continue
				}
				request, err := permissionRequest(stateDir, n)
				if err != nil || request.P {
					return decision(strconv.FormatInt(n, 10), request), true
				}
			}
		}
		raw := strings.ToLower(action.Target)
		if unknownNumber || uncertain && strings.Contains(raw, "duolog") && strings.Contains(raw, "allow") {
			if request, pending, err := pendingPersonAsk(stateDir); err != nil || pending {
				return decision("", request), true
			}
		}
	}
	return gate.Decision{}, false
}

// leadAllowRequest returns zero with true when an allow names no literal number.
func leadAllowRequest(part string) (int64, bool) {
	args, ok := leadCommandArgs(part, "allow")
	if !ok {
		return 0, false
	}
	for i := 0; i < len(args); i++ {
		if args[i] == "--lead" {
			i++
			continue
		}
		if args[i] == "--" || strings.HasPrefix(args[i], "--lead=") {
			continue
		}
		if n, err := strconv.ParseInt(args[i], 10, 64); err == nil && n > 0 {
			return n, true
		}
	}
	return 0, true
}

func leadCommandArgs(part, verb string) ([]string, bool) {
	words := strings.Fields(strings.NewReplacer("'", "", "\"", "", "`", "").Replace(part))
	duolog := false
	for i := 0; i < len(words); i++ {
		name := strings.ToLower(path.Base(pathutil.Slashes(words[i])))
		if name == "duolog" || name == "duolog.exe" {
			duolog = true
			continue
		}
		if !duolog {
			continue
		}
		switch {
		case words[i] == "--lead":
			i++
		case words[i] == "--" || strings.HasPrefix(words[i], "--lead="):
		case words[i] == verb:
			return words[i+1:], true
		}
	}
	return nil, false
}

func pendingPersonAsk(stateDir string) (bus.Message, bool, error) {
	f, err := os.Open(filepath.Join(stateDir, "bus.ndjson"))
	if err != nil {
		return bus.Message{}, false, err
	}
	defer f.Close()
	pending := map[string]bus.Message{}
	decoder := json.NewDecoder(f)
	for {
		var m bus.Message
		if err := decoder.Decode(&m); err == io.EOF {
			break
		} else if err != nil {
			return bus.Message{}, false, err
		}
		if m.K == gate.Ask && m.P && !gate.AskExpired(m) {
			pending[strconv.FormatInt(m.N, 10)] = m
		}
		if m.K == gate.Allow || m.K == gate.Deny {
			delete(pending, m.Ref)
		}
	}
	var request bus.Message
	for _, m := range pending {
		if request.N == 0 || m.N < request.N {
			request = m
		}
	}
	return request, len(pending) > 0, nil
}
