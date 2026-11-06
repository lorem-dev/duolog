package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/project"
)

// guardLeadCall is what the lead hook runs for one tool call: the
// person-only guards of leadGuard, unless the lead decides by itself and
// every person-only ask the command can allow is its own. A command that
// may allow another lead's ask, with --lead or without, still asks the
// person: --lead names a lead and proves nothing about who runs the command.
func guardLeadCall(stateDir, agent string, input []byte) (gate.Decision, bool) {
	d, ok := leadGuard(stateDir, agent, input)
	if !ok {
		return d, false
	}
	if lead, decides := leadDecides(stateDir, agent, input); decides && staysInProject(stateDir, agent, input) && ownsAll(lead, leadAllowAsks(stateDir, agent, input)) {
		return gate.Decision{}, false
	}
	return d, true
}

// staysInProject reports whether every duolog command in the lead's call runs
// in the hook's own project, whose bus the guard read: the hook's cwd is in
// that project, the line reads for sure (leadWords), no part changes
// directory or the environment for later parts or names another folder with
// -C or a --project, --dir or state flag, and a part that names duolog starts
// with duolog, not a wrapper. An allow, deny or answer then carries only ask
// numbers and --lead. Anything else may answer an ask in another project,
// where owning an ask of the same number proves nothing.
func staysInProject(stateDir, agent string, input []byte) bool {
	actions, hookCwd, err := leadActions(agent, input)
	if err != nil {
		return false
	}
	if hookCwd != "" && filepath.Clean(project.StateRoot(hookCwd)) != filepath.Clean(filepath.Dir(stateDir)) {
		return false
	}
	for _, action := range actions {
		if action.Kind != "exec" {
			continue
		}
		parts, words, ok := leadWords(action.Target, action.Shell)
		if !ok || len(parts) == 0 {
			return false
		}
		for i, part := range parts {
			if !plainPart(part, words[i]) {
				return false
			}
		}
	}
	return true
}

// quoteMark wraps the number of a quoted run in a masked line (maskQuotes).
// A line that holds it itself is not read.
const quoteMark = "\x01"

// leadWords splits a lead's command into the gate's parts and, for each part,
// the words the shell passes on. The gate's parser takes any quoted word with
// spaces or a glob as uncertain, which its matchers need. Here such a line is
// read again with each quoted run masked (maskQuotes): quoted text then
// cannot add a separator, a subshell, a redirect or a flag of its own, and
// stays one literal word. ok is false when the line still does not read for
// sure, such as one with an expansion, a backslash or an open quote.
func leadWords(command, shell string) (parts []string, words [][]string, ok bool) {
	parts, uncertain := gate.ShellParts(command, shell)
	unmask := strings.NewReplacer()
	if uncertain {
		if shell == "powershell" || strings.Contains(command, quoteMark) {
			return nil, nil, false
		}
		masked, runs, literal := maskQuotes(command)
		if !literal {
			return nil, nil, false
		}
		if parts, uncertain = gate.ShellParts(masked, shell); uncertain {
			return nil, nil, false
		}
		pairs := make([]string, 0, 2*len(runs))
		for i, run := range runs {
			pairs = append(pairs, quoteMark+strconv.Itoa(i)+quoteMark, run)
		}
		unmask = strings.NewReplacer(pairs...)
	}
	// A masked line has no quote left outside its runs, so the strip takes
	// only the quotes the gate's parser kept in a line it read for sure.
	strip := strings.NewReplacer("'", "", "\"", "", "`", "")
	for _, part := range parts {
		var w []string
		for _, field := range strings.Fields(part) {
			w = append(w, unmask.Replace(strip.Replace(field)))
		}
		words = append(words, w)
	}
	return parts, words, true
}

// maskQuotes replaces each quoted run of command, quotes included, with its
// number between two quoteMarks, and returns the runs' literal text. A
// single-quoted run is always literal; a double-quoted run is literal only
// without $, ` or \, which the shell reads inside it. literal is false for
// any other run and for an open quote.
func maskQuotes(command string) (masked string, runs []string, literal bool) {
	var b strings.Builder
	for i := 0; i < len(command); i++ {
		c := command[i]
		if c != '\'' && c != '"' {
			b.WriteByte(c)
			continue
		}
		end := strings.IndexByte(command[i+1:], c)
		if end < 0 {
			return "", nil, false
		}
		run := command[i+1 : i+1+end]
		if c == '"' && strings.ContainsAny(run, "$`\\") {
			return "", nil, false
		}
		b.WriteString(quoteMark + strconv.Itoa(len(runs)) + quoteMark)
		runs = append(runs, run)
		i += end + 1
	}
	return b.String(), runs, true
}

// leavingWords start a part that changes the folder or the environment of the
// parts after it.
var leavingWords = []string{"cd", "chdir", "pushd", "popd", "set-location", "sl", "push-location", "pop-location",
	"export", "set", "unset", "setenv", "source", ".", "alias", "declare", "typeset", "local", "readonly"}

// folderFlags name another folder for the command they are given to.
var folderFlags = []string{"--project", "--dir", "--directory", "--chdir", "--cwd", "--root", "--state", "--state-dir", "--git-dir", "--work-tree"}

// runNextWords run the word after them as the command: builtin, and zsh's
// noglob and nocorrect. The gate's parser keeps them as the first word.
var runNextWords = []string{"builtin", "noglob", "nocorrect"}

// plainPart is one part of a plain lead call, with the words the shell
// passes on; see staysInProject. A quoted run of part is masked, so a
// parenthesis in it opens no subshell. A runNextWords prefix is skipped, and
// the word after it judged.
func plainPart(part string, words []string) bool {
	for len(words) > 1 && slices.Contains(runNextWords, strings.ToLower(words[0])) {
		words = words[1:]
	}
	if len(words) == 0 {
		return true
	}
	if leavesFolder(part, words) {
		return false
	}
	if !strings.Contains(strings.ToLower(strings.Join(words, " ")), "duolog") {
		return true
	}
	if gate.ProgramName(words[0]) != "duolog" {
		return false
	}
	for _, verb := range []string{"allow", "deny", "answer"} {
		if args, ok := commandArgs(words, verb); ok && !onlyAskNumbers(args) {
			return false
		}
	}
	return true
}

// leavesFolder reports a part that changes the folder or the environment of
// the parts after it, opens a subshell, or names another folder with -C or a
// folder flag.
func leavesFolder(part string, words []string) bool {
	first := strings.ToLower(words[0])
	if slices.Contains(leavingWords, first) || strings.Contains(first, "=") || strings.HasPrefix(first, "$env:") || strings.ContainsAny(part, "()") {
		return true
	}
	return slices.ContainsFunc(words[1:], func(w string) bool {
		flag, _, _ := strings.Cut(strings.ToLower(w), "=")
		return strings.HasPrefix(w, "-C") || slices.Contains(folderFlags, flag)
	})
}

// onlyAskNumbers reports whether the arguments of an allow, deny or answer
// are ask numbers and --lead only.
func onlyAskNumbers(args []string) bool {
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--lead":
			i++
		case strings.HasPrefix(args[i], "--lead="):
		default:
			if n, err := strconv.ParseInt(args[i], 10, 64); err != nil || n <= 0 {
				return false
			}
		}
	}
	return true
}

// ownsAll reports whether every ask is addressed to the lead. An ask that
// could not be read is a zero message, which no lead owns.
func ownsAll(lead session.Lead, asks []bus.Message) bool {
	address := session.Address(lead)
	for _, ask := range asks {
		if ask.To == "" || ask.To != address {
			return false
		}
	}
	return true
}

// selfDeciding names the Claude modes in which the person lets the lead model
// decide without asking: bypass and auto.
func selfDeciding(native string) bool { return native == "bypassPermissions" || native == "auto" }

// leadDecides reports whether a Claude lead runs in bypassPermissions or auto,
// so that the person-only guards leave the call to the lead model, and
// returns that lead. Two records must say so: this call's hook payload, which
// Claude Code writes to the hook's stdin, and the native mode duolog recorded
// for the same registered lead from its earlier main-thread payloads. A
// session that is not a registered lead (a worker's or a peer's), a worker or
// peer process, a subagent of the lead (its payload carries its agent_id), a
// state folder that does not exist yet, and a Codex lead, whose mode comes
// from a transcript file, never qualify. No environment variable or file
// can raise the answer: they only take it away.
func leadDecides(stateDir, agent string, input []byte) (session.Lead, bool) {
	if agent != "claude" || isWorker() || os.Getenv("DUOLOG_PEER") != "" {
		return session.Lead{}, false
	}
	var h struct {
		ID      string `json:"session_id"`
		Mode    string `json:"permission_mode"`
		AgentID string `json:"agent_id"`
	}
	if json.Unmarshal(input, &h) != nil || h.ID == "" || h.AgentID != "" || !selfDeciding(h.Mode) {
		return session.Lead{}, false
	}
	// session.Open would create the folder; a project without state has no lead.
	if info, err := os.Stat(stateDir); err != nil || !info.IsDir() {
		return session.Lead{}, false
	}
	reg, err := session.Open(stateDir)
	if err != nil {
		return session.Lead{}, false
	}
	lead, ok, err := reg.Lead(h.ID)
	if err != nil || !ok || lead.Agent != "claude" || !selfDeciding(lead.Native) {
		return session.Lead{}, false
	}
	return lead, true
}

// leadGuard keeps for the person what only the person may do: allowing an
// ask that reads or writes outside the project, approving a rule outside it,
// duolog peer show, and telemetry and balance data. A Claude lead's call is
// asked; a Codex lead's call is denied with a pointer to the person's
// terminal. The hook runs it through guardLeadCall.
func leadGuard(stateDir, agent string, input []byte) (gate.Decision, bool) {
	actions, hookCwd, err := leadActions(agent, input)
	if err != nil {
		return gate.Decision{}, false
	}
	// Telemetry and balance are the person's: the paths are placed as the
	// worker gate places them, against the work root and the hook's cwd.
	root := filepath.Dir(stateDir)
	cwd := hookCwd
	if cwd == "" {
		cwd = root
	}
	work := project.WorkRoot(cwd, root)
	placed := make([]gate.Action, len(actions))
	for i, action := range actions {
		action.Work, action.Cwd = work, cwd
		if (action.Kind == "read" || action.Kind == "write") && action.Target != "" {
			action.Target = gate.RelativeTarget(work, hookCwd, action.Target)
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
		if allowMayHide(action.Target, unknownNumber, uncertain) {
			if request, pending, err := pendingPersonAsk(stateDir); err != nil || pending {
				return decision("", request), true
			}
		}
	}
	return gate.Decision{}, false
}

// leadActions is the lead call's actions, with the Bash command as one
// exec action when the tool input names one and no action is an exec, and
// the hook's cwd.
func leadActions(agent string, input []byte) ([]gate.Action, string, error) {
	actions, err := gate.NormalizeAll(agent, input)
	if err != nil {
		return nil, "", err
	}
	var envelope struct {
		Cwd   string `json:"cwd"`
		Input struct {
			Command json.RawMessage `json:"command"`
		} `json:"tool_input"`
	}
	var command string
	if json.Unmarshal(input, &envelope) == nil && json.Unmarshal(envelope.Input.Command, &command) == nil && command != "" &&
		!slices.ContainsFunc(actions, func(a gate.Action) bool { return a.Kind == "exec" }) {
		actions = append(actions, gate.Action{Kind: "exec", Target: command})
	}
	return actions, envelope.Cwd, nil
}

// allowMayHide reports whether a command may allow an ask whose number it
// does not name: an allow with no literal number, or an uncertain command
// that names duolog and allow anywhere.
func allowMayHide(command string, unknownNumber, uncertain bool) bool {
	raw := strings.ToLower(command)
	return unknownNumber || uncertain && strings.Contains(raw, "duolog") && strings.Contains(raw, "allow")
}

// leadAllowAsks returns every person-only ask the lead's call can allow,
// by the rule of leadGuard: the ask of each literal number, and every
// pending person-only ask when an allow names no number or the command is
// uncertain. An ask that cannot be read is a zero message.
func leadAllowAsks(stateDir, agent string, input []byte) []bus.Message {
	actions, _, err := leadActions(agent, input)
	if err != nil {
		return []bus.Message{{}}
	}
	var asks []bus.Message
	for _, action := range actions {
		if action.Kind != "exec" {
			continue
		}
		parts, uncertain := gate.ShellParts(action.Target, action.Shell)
		if uncertain {
			parts = append(parts, action.Target)
		}
		unknownNumber := false
		for _, part := range parts {
			n, ok := leadAllowRequest(part)
			if ok && n == 0 {
				unknownNumber = true
			} else if ok {
				asks = append(asks, personAsk(stateDir, n)...)
			}
		}
		if allowMayHide(action.Target, unknownNumber, uncertain) {
			pending, err := pendingPersonAsks(stateDir)
			if err != nil {
				pending = []bus.Message{{}}
			}
			asks = append(asks, pending...)
		}
	}
	return asks
}

// personAsk returns ask n when it is person-only, a zero message when it
// cannot be read, and nothing otherwise.
func personAsk(stateDir string, n int64) []bus.Message {
	request, err := permissionRequest(stateDir, n)
	if err != nil {
		return []bus.Message{{}}
	}
	if request.P {
		return []bus.Message{request}
	}
	return nil
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
	return commandArgs(strings.Fields(strings.NewReplacer("'", "", "\"", "", "`", "").Replace(part)), verb)
}

// commandArgs returns the words after verb in duolog [--lead id] verb.
func commandArgs(words []string, verb string) ([]string, bool) {
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

// pendingPersonAsk returns the oldest pending person-only ask, if any.
func pendingPersonAsk(stateDir string) (bus.Message, bool, error) {
	asks, err := pendingPersonAsks(stateDir)
	if err != nil || len(asks) == 0 {
		return bus.Message{}, false, err
	}
	return asks[0], true, nil
}

// pendingPersonAsks returns every person-only ask that is still unanswered
// and unexpired, oldest first.
func pendingPersonAsks(stateDir string) ([]bus.Message, error) {
	f, err := os.Open(filepath.Join(stateDir, "bus.ndjson"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	pending := map[string]bus.Message{}
	decoder := json.NewDecoder(f)
	for {
		var m bus.Message
		if err := decoder.Decode(&m); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		if m.K == gate.Ask && m.P && !gate.AskExpired(m) {
			pending[strconv.FormatInt(m.N, 10)] = m
		}
		if m.K == gate.Allow || m.K == gate.Deny {
			delete(pending, m.Ref)
		}
	}
	asks := slices.Collect(maps.Values(pending))
	slices.SortFunc(asks, func(a, b bus.Message) int { return cmp.Compare(a.N, b.N) })
	return asks, nil
}
