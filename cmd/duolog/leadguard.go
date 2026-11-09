package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/gate"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/core/policy"
	"github.com/lorem-dev/duolog/internal/project"
)

// guardLeadCall is what the lead hook runs for one tool call: the
// person-only guards of leadGuard, unless the lead decides by itself and
// every person-only ask the command can allow is its own. A command that
// may allow another lead's ask, with --lead or without, still asks the
// person: --lead names a lead and proves nothing about who runs the command.
func guardLeadCall(stateDir, agent string, input []byte) (policy.Decision, bool) {
	input = mcpAnswerInput(input)
	d, ok := leadGuard(stateDir, agent, input)
	if !ok {
		return d, false
	}
	if lead, decides := leadDecides(stateDir, agent, input); decides && staysInProject(stateDir, agent, input) && ownsAll(lead, leadAllowAsks(stateDir, agent, input)) {
		return policy.Decision{}, false
	}
	return d, true
}

// answerAskTool is the Claude name of the MCP server's answer_ask tool, as
// the lead's hook sees it (mcp.AnswerAskTool).
const answerAskTool = "mcp__duolog__answer_ask"

// mcpAnswerInput rewrites a lead's mcp__duolog__answer_ask call as the Bash
// call `duolog allow|deny <n>` that the guard already judges, keeping cwd.
// An ask that is not a positive integer, or a decision other than allow or
// deny, becomes `duolog allow` with no number, which the guard treats as one
// that may hide a person-only ask. Any other input comes back unchanged.
func mcpAnswerInput(input []byte) []byte {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(input, &envelope) != nil {
		return input
	}
	var tool string
	if json.Unmarshal(envelope["tool_name"], &tool) != nil || tool != answerAskTool {
		return input
	}
	// The other envelope keys (session_id, permission_mode, agent_id, cwd)
	// stay as they came: leadDecides and staysInProject read them.
	command := "duolog allow"
	var call struct {
		Ask      json.RawMessage `json:"ask"`
		Decision json.RawMessage `json:"decision"`
	}
	var decision string
	if json.Unmarshal(envelope["tool_input"], &call) == nil && json.Unmarshal(call.Decision, &decision) == nil && (decision == "allow" || decision == "deny") {
		// A JSON string, fraction or exponent does not parse here.
		if n, err := strconv.ParseInt(string(call.Ask), 10, 64); err == nil && n > 0 {
			command = "duolog " + decision + " " + strconv.FormatInt(n, 10)
		}
	}
	name, err := json.Marshal("Bash")
	if err != nil {
		return input
	}
	toolInput, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		return input
	}
	envelope["tool_name"], envelope["tool_input"] = name, toolInput
	out, err := json.Marshal(envelope)
	if err != nil {
		return input
	}
	return out
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
	parts, uncertain := policy.ShellParts(command, shell)
	unmask := strings.NewReplacer()
	if uncertain {
		if shell == "powershell" || strings.Contains(command, quoteMark) {
			return nil, nil, false
		}
		masked, runs, literal := maskQuotes(command)
		if !literal {
			return nil, nil, false
		}
		if parts, uncertain = policy.ShellParts(masked, shell); uncertain {
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
	if policy.ProgramName(words[0]) != "duolog" {
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
func leadGuard(stateDir, agent string, input []byte) (policy.Decision, bool) {
	actions, hookCwd, err := leadActions(agent, input)
	if err != nil {
		return policy.Decision{}, false
	}
	// Telemetry and balance are the person's: the paths are placed as the
	// worker gate places them, against the work root and the hook's cwd.
	root := filepath.Dir(stateDir)
	cwd := hookCwd
	if cwd == "" {
		cwd = root
	}
	work := project.WorkRoot(cwd, root)
	placed := make([]policy.Action, len(actions))
	for i, action := range actions {
		action.Work, action.Cwd = work, cwd
		if (action.Kind == "read" || action.Kind == "write") && action.Target != "" {
			action.Target = policy.RelativeTarget(work, hookCwd, action.Target)
		}
		placed[i] = action
	}
	if d, ok := telemetryLeadGuard(agent, placed); ok {
		return d, true
	}
	if d, ok := peerShowLeadGuard(agent, placed); ok {
		return d, true
	}
	decision := func(number string, request bus.Message) policy.Decision {
		kind := policy.Ask
		reason := "this command may allow an ask that reads or writes outside the project; only the person may allow it"
		if number != "" {
			reason = fmt.Sprintf("ask %s reads or writes outside the project; only the person may allow it", number)
		}
		if agent == "codex" {
			kind = policy.Deny
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
		return policy.Decision{Kind: kind, Reason: reason, Rule: "person-only ask"}
	}
	for _, action := range actions {
		if action.Kind != "exec" {
			continue
		}
		parts, uncertain := policy.ShellParts(action.Target, action.Shell)
		if uncertain {
			// ShellParts can omit wrappers such as env; inspect their tokens too.
			parts = append(parts, action.Target)
		}
		unknownNumber := false
		for _, part := range parts {
			if args, ok := leadCommandArgs(part, "remember"); ok {
				if policy.ExecOutside(policy.Action{Kind: "exec", Target: strings.Join(args, " "), Work: work, Cwd: cwd, Shell: action.Shell}) {
					d := policy.Decision{Kind: policy.Ask, Rule: "person-only ask", Reason: "this command may approve a rule outside the project; only the person may allow it"}
					if agent == "codex" {
						d.Kind = policy.Deny
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
	return policy.Decision{}, false
}

// leadActions is the lead call's actions, with the Bash command as one
// exec action when the tool input names one and no action is an exec, and
// the hook's cwd.
func leadActions(agent string, input []byte) ([]policy.Action, string, error) {
	actions, err := policy.NormalizeAll(agent, input)
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
		!slices.ContainsFunc(actions, func(a policy.Action) bool { return a.Kind == "exec" }) {
		actions = append(actions, policy.Action{Kind: "exec", Target: command})
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
		parts, uncertain := policy.ShellParts(action.Target, action.Shell)
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

// peerShowLeadGuard stops a lead's tool call that names duolog peer show, or
// duolog watch without --tab, anywhere in its command: behind env, script,
// rex, tmux, sh -c or any other wrapper, since a model that starts it under a
// pty with a cleaned environment passes their own checks. A Claude lead's
// call asks the person, unless the lead runs in bypass or auto
// (guardLeadCall); a Codex lead's call is denied. It guards against
// accidents: no check can prove that a terminal is the person's. duolog runs
// and duolog watch --tab print no worker output, so they pass.
func peerShowLeadGuard(agent string, actions []policy.Action) (policy.Decision, bool) {
	for _, a := range actions {
		if a.Kind != "exec" {
			continue
		}
		// ShellParts splits lists and pipelines; the whole text also counts,
		// because ShellParts does not open every wrapper.
		parts, _ := policy.ShellParts(a.Target, a.Shell)
		texts := slices.Concat(parts, []string{a.Target})
		switch {
		case slices.ContainsFunc(texts, namesPeerShow):
			if agent == "codex" {
				return policy.Decision{Kind: policy.Deny, Rule: "person-only peer show", Reason: "duolog peer show prints a peer's screen for the person only: they run it in their own terminal; use duolog peer view <id> or peer list --long"}, true
			}
			return policy.Decision{Kind: policy.Ask, Rule: "person-only peer show", Reason: "duolog peer show prints a peer's screen for the person only; the person confirms this call"}, true
		case namesWatch(a.Target, a.Shell):
			if agent == "codex" {
				return policy.Decision{Kind: policy.Deny, Rule: "person-only watch", Reason: "duolog watch prints a worker's stream for the person only: they run it in their own terminal; use duolog runs, or duolog watch --tab <id> when they ask to see a run"}, true
			}
			return policy.Decision{Kind: policy.Ask, Rule: "person-only watch", Reason: "duolog watch prints a worker's stream for the person only; the person confirms this call"}, true
		}
	}
	return policy.Decision{}, false
}

// peerShowWords splits command text into words, with quotes removed and
// shell operators taken as spaces, so that duolog inside sh -c '...', $(...)
// or a && list is a word of its own.
var peerShowWords = strings.NewReplacer("'", "", `"`, "", "`", "", ";", " ", "&", " ", "|", " ", "(", " ", ")", " ", "<", " ", ">", " ", "$", " ", "{", " ", "}", " ")

// namesPeerShow reports whether text holds duolog [flags] peer [flags] show.
func namesPeerShow(text string) bool {
	words := strings.Fields(peerShowWords.Replace(text))
	for i, w := range words {
		if policy.ProgramName(w) != "duolog" {
			continue
		}
		j := skipDuologFlags(words, i+1)
		if j >= len(words) || words[j] != "peer" {
			continue
		}
		if j = skipDuologFlags(words, j+1); j < len(words) && words[j] == "show" {
			return true
		}
	}
	return false
}

// skipDuologFlags returns the index of the first word from j that is not a
// flag; --lead takes the next word.
func skipDuologFlags(words []string, j int) int {
	for j < len(words) && strings.HasPrefix(words[j], "-") {
		if words[j] == "--lead" {
			j++
		}
		j++
	}
	return j
}

// reviewChars are the characters that make the gate's parser set a command
// aside for review and leave it out of its parts when they sit in its first
// word: an assignment, a glob, a home folder or a .. path. namesWatch hides
// each behind a marker while it splits the text, and puts it back after.
var reviewChars = []string{"=", "*", "?", "[", "]", "~", ".."}

var reviewMask, reviewUnmask = func() (*strings.Replacer, *strings.Replacer) {
	var mask, unmask []string
	for i, c := range reviewChars {
		marker := "\x02" + strconv.Itoa(i) + "\x02"
		mask, unmask = append(mask, c, marker), append(unmask, marker, c)
	}
	return strings.NewReplacer(mask...), strings.NewReplacer(unmask...)
}()

// watchWords strips what wraps a word in a segment: quotes, redirects and the
// dollar sign of an expansion, and keeps each subshell or brace mark as a ";"
// word, so that the command inside starts a command.
var watchWords = strings.NewReplacer("'", "", `"`, "", "`", " ; ", "(", " ; ", ")", " ; ", "<", " ", ">", " ", "$", " ", "{", " ; ", "}", " ; ")

// shellWrappers run the text after -c as a command line of their own.
var shellWrappers = []string{"sh", "bash", "zsh", "dash"}

// plainWrappers run the command after them and take only flags and NAME=value
// words before it; looseWrappers may take any words (a file, a duration, a
// subcommand) first.
var (
	plainWrappers = []string{"env", "sh", "bash", "zsh", "dash", "command", "exec", "nohup", "time", "setsid", "stdbuf"}
	looseWrappers = []string{"script", "timeout", "nice", "tmux", "rex", "sudo", "xargs"}
)

// namesWatch reports whether text runs duolog [flags] watch with no --tab
// among the words that follow it in the same command. The gate's shell parser
// splits text into commands, so a separator inside quotes is no break. A
// command must start its segment, after any leading NAME=value words, or
// follow a known wrapper, so a search for the text or a commit message that
// holds it does not count. A word that starts a comment ends the command, and
// only --tab or --tab=true counts as --tab, not as the value of --lead or
// --client. watch --tab prints one line and opens a Rex tab, so it passes, as
// peer view does.
func namesWatch(text, shell string) bool {
	// A quoted run is masked while the text is split (maskQuotes), so that no
	// quoted character breaks a command; a text with a run that is not literal
	// is split as it is.
	masked, runs, literal := maskQuotes(text)
	if !literal || strings.Contains(text, quoteMark) {
		masked, runs = text, nil
	}
	pairs := make([]string, 0, 2*len(runs))
	for i, run := range runs {
		pairs = append(pairs, quoteMark+strconv.Itoa(i)+quoteMark, run)
	}
	parts, _ := policy.ShellParts(reviewMask.Replace(masked), shell)
	for _, part := range parts {
		var words []string
		for _, word := range strings.Fields(watchWords.Replace(reviewUnmask.Replace(part))) {
			// A run that is one plain word, such as "watch", is that word; any
			// other run stays masked and is no command word.
			if plain := strings.NewReplacer(pairs...).Replace(word); !strings.ContainsAny(plain, " \t\n;|&()<>$`") {
				word = plain
			}
			words = append(words, word)
		}
		if watchSegment(words, shell, strings.NewReplacer(pairs...)) {
			return true
		}
	}
	return false
}

// watchSegment is namesWatch for the words of one command segment; unmask
// puts back the quoted runs the words hold.
func watchSegment(words []string, shell string, unmask *strings.Replacer) bool {
	start := true
	for i := 0; i < len(words); i++ {
		if words[i] == ";" {
			start = true
			continue
		}
		if !start {
			continue
		}
		// A quoted run in command position is a command line for a shell.
		if strings.HasPrefix(words[i], quoteMark) {
			if namesWatch(unmask.Replace(words[i]), shell) {
				return true
			}
			continue
		}
		// A leading NAME=value word counts like a wrapper.
		if strings.Contains(words[i], "=") {
			continue
		}
		name := strings.ToLower(policy.ProgramName(words[i]))
		if name == "duolog" && unguardedWatch(words, i) {
			return true
		}
		next, wrapper := skipWrapper(words, i, name)
		// The text after sh -c is a command line of its own, whose separators
		// the quotes around it hid from the split.
		if runsScript(name, words[i+1:next+1]) {
			return namesWatch(unmask.Replace(strings.Join(words[next+1:], " ")), shell)
		}
		i, start = next, wrapper
	}
	return false
}

// unguardedWatch reports whether the duolog at words[i] runs watch without --tab.
func unguardedWatch(words []string, i int) bool {
	j := skipDuologFlags(words, i+1)
	return j < len(words) && words[j] == "watch" && !hasTab(words[j+1:])
}

// runsScript reports whether the shell wrapper name, with these flag words,
// takes the text after them as a command line (-c, -lc).
func runsScript(name string, flags []string) bool {
	return slices.Contains(shellWrappers, name) && slices.ContainsFunc(flags, func(w string) bool {
		return strings.HasPrefix(w, "-") && strings.HasSuffix(w, "c")
	})
}

// skipWrapper returns the index of the last word that belongs to the wrapper
// named name at words[i], and whether name is a wrapper, so that the word
// after it starts a command.
func skipWrapper(words []string, i int, name string) (int, bool) {
	switch {
	case slices.Contains(plainWrappers, name):
		for i+1 < len(words) && (strings.HasPrefix(words[i+1], "-") || strings.Contains(words[i+1], "=")) {
			i++
			// env -u NAME and env -C DIR take the next word.
			if name == "env" && slices.Contains([]string{"-u", "-C", "-S", "-P", "--unset", "--chdir"}, words[i]) {
				i++
			}
		}
		return i, true
	case slices.Contains(looseWrappers, name):
		// The next word may be anything; the one that names duolog counts.
		for i+1 < len(words) && words[i+1] != ";" && policy.ProgramName(words[i+1]) != "duolog" {
			i++
		}
		return i, true
	}
	return i, false
}

// hasTab reports whether the words of a watch command, up to its end or a comment, hold
// --tab or --tab=true as a flag. The word after --lead or --client is that
// flag's value.
func hasTab(rest []string) bool {
	for i := 0; i < len(rest); i++ {
		switch w := rest[i]; {
		case w == ";" || strings.HasPrefix(w, "#"):
			return false
		case w == "--lead" || w == "--client":
			i++
		case w == "--tab" || w == "--tab=true":
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
		if policy.ProgramName(words[i]) == "duolog" {
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
	asks, err := gate.OpenAsks(stateDir)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(asks, func(m bus.Message) bool { return !m.P }), nil
}
