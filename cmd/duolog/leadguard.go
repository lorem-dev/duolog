package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/session"
)

// guardLeadCall is what the lead hook runs for one tool call: the
// person-only guards of leadGuard, unless the lead decides by itself.
func guardLeadCall(stateDir, agent string, input []byte) (gate.Decision, bool) {
	d, ok := leadGuard(stateDir, agent, input)
	if ok && leadDecides(stateDir, agent, input) {
		return gate.Decision{}, false
	}
	return d, ok
}

// selfDeciding names the Claude modes in which the person lets the lead model
// decide without asking: bypass and auto.
func selfDeciding(native string) bool { return native == "bypassPermissions" || native == "auto" }

// leadDecides reports whether a Claude lead runs in bypassPermissions or auto,
// so that the person-only guards leave the call to the lead model. Two
// records must say so: this call's hook payload, which Claude Code writes to
// the hook's stdin, and the native mode duolog recorded for the same
// registered lead from its earlier payloads. A session that is not a
// registered lead (a worker's or a peer's), a worker or peer process, a
// state folder that does not exist yet, and a Codex lead, whose mode comes
// from a transcript file, never qualify. No environment variable or file
// can raise the answer: they only take it away.
func leadDecides(stateDir, agent string, input []byte) bool {
	if agent != "claude" || isWorker() || os.Getenv("DUOLOG_PEER") != "" {
		return false
	}
	var h struct {
		ID   string `json:"session_id"`
		Mode string `json:"permission_mode"`
	}
	if json.Unmarshal(input, &h) != nil || h.ID == "" || !selfDeciding(h.Mode) {
		return false
	}
	// session.Open would create the folder; a project without state has no lead.
	if info, err := os.Stat(stateDir); err != nil || !info.IsDir() {
		return false
	}
	reg, err := session.Open(stateDir)
	if err != nil {
		return false
	}
	lead, ok, err := reg.Lead(h.ID)
	return err == nil && ok && lead.Agent == "claude" && selfDeciding(lead.Native)
}

// leadGuard keeps for the person what only the person may do: allowing an
// ask that reads or writes outside the project, approving a rule outside it,
// duolog peer show, and telemetry and balance data. A Claude lead's call is
// asked; a Codex lead's call is denied with a pointer to the person's
// terminal. The hook runs it through guardLeadCall.
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
	// Telemetry and balance are the person's: the paths are placed as the
	// worker gate places them, against the work root and the hook's cwd.
	root := filepath.Dir(stateDir)
	cwd := envelope.Cwd
	if cwd == "" {
		cwd = root
	}
	work := project.WorkRoot(cwd, root)
	placed := make([]gate.Action, len(actions))
	for i, action := range actions {
		action.Work, action.Cwd = work, cwd
		if (action.Kind == "read" || action.Kind == "write") && action.Target != "" {
			action.Target = gate.RelativeTarget(work, envelope.Cwd, action.Target)
		}
		placed[i] = action
	}
	if d, ok := telemetryLeadGuard(agent, placed); ok {
		return d, true
	}
	if d, ok := peerShowLeadGuard(agent, placed); ok {
		return d, true
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

// peerShowLeadGuard stops a lead's tool call that names duolog peer show
// anywhere in its command: behind env, script, rex, tmux, sh -c or any other
// wrapper, since a model that starts it under a pty with a cleaned
// environment passes peer show's own checks. A Claude lead's call asks the
// person, unless the lead runs in bypass or auto (guardLeadCall); a Codex
// lead's call is denied. It guards against accidents: no
// check can prove that a terminal is the person's.
func peerShowLeadGuard(agent string, actions []gate.Action) (gate.Decision, bool) {
	for _, a := range actions {
		if a.Kind != "exec" {
			continue
		}
		// ShellParts splits lists and pipelines; the whole text also counts,
		// because ShellParts does not open every wrapper.
		parts, _ := gate.ShellParts(a.Target, a.Shell)
		if !slices.ContainsFunc(append(parts, a.Target), namesPeerShow) {
			continue
		}
		if agent == "codex" {
			return gate.Decision{Kind: gate.Deny, Rule: "person-only peer show", Reason: "duolog peer show prints a peer's screen for the person only: they run it in their own terminal; use duolog peer view <id> or peer list --long"}, true
		}
		return gate.Decision{Kind: gate.Ask, Rule: "person-only peer show", Reason: "duolog peer show prints a peer's screen for the person only; the person confirms this call"}, true
	}
	return gate.Decision{}, false
}

// peerShowWords splits command text into words, with quotes removed and
// shell operators taken as spaces, so that duolog inside sh -c '...', $(...)
// or a && list is a word of its own.
var peerShowWords = strings.NewReplacer("'", "", `"`, "", "`", "", ";", " ", "&", " ", "|", " ", "(", " ", ")", " ", "<", " ", ">", " ", "$", " ", "{", " ", "}", " ")

// namesPeerShow reports whether text holds duolog [flags] peer [flags] show.
func namesPeerShow(text string) bool {
	words := strings.Fields(peerShowWords.Replace(text))
	// skip passes duolog's own flags; --lead takes the next word.
	skip := func(j int) int {
		for j < len(words) && strings.HasPrefix(words[j], "-") {
			if words[j] == "--lead" {
				j++
			}
			j++
		}
		return j
	}
	for i, w := range words {
		if gate.ProgramName(w) != "duolog" {
			continue
		}
		j := skip(i + 1)
		if j >= len(words) || words[j] != "peer" {
			continue
		}
		if j = skip(j + 1); j < len(words) && words[j] == "show" {
			return true
		}
	}
	return false
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
		if gate.ProgramName(words[i]) == "duolog" {
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
