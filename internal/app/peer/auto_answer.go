package peer

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/gate"
	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/policy"
	corescreen "github.com/lorem-dev/duolog/internal/core/screen"
	"github.com/lorem-dev/duolog/internal/store"
)

// autoAnswer gives command prompts to the gate and declines Claude's plugin
// offers before considering text rules. A missing command or option leaves the
// question for the lead, and a plugin install question never reaches text rules.
func (e *env) autoAnswer(ctx context.Context, entry session.Entry, screen Screen) (bool, error) {
	pane := e.pane(entry.Agent)
	if answered, handled, err := e.agentQuestion(ctx, entry, screen, pane.Agent); handled {
		return answered, err
	}
	// A billing choice, such as using an API key the agent found, is the
	// lead's to answer, whatever auto_answers match.
	if billingQuestion(screen) {
		return false, nil
	}
	chosen, ok, err := e.chooseAnswer(entry, screen, pane.Facts)
	if err != nil || !ok {
		return false, err
	}
	target := optionFor(screen, chosen.label)
	if target < 0 || !config.SafeAutoAnswer(screen.Options[target].Label) {
		return false, nil
	}
	if err := e.selectOption(ctx, entry, screen, screen.Options[target].Label); err != nil {
		return false, err
	}
	if chosen.action.Kind == "answer" {
		chosen.action.Target = screen.Question + " => " + screen.Options[target].Label
	}
	if err := gate.Log(&store.Store{Dir: e.dir}, chosen.action, chosen.decision); err != nil {
		return false, err
	}
	_, err = e.update(entry.ID, func(x *session.Entry) { x.Asked = "" })
	return true, err
}

// answerChoice is the option label an automatic answer picks, with the action
// and decision that the gate log records for it.
type answerChoice struct {
	label    string
	action   policy.Action
	decision policy.Decision
}

// chooseAnswer picks the label to answer with: the gate's verdict on the
// command a prompt asks about, or the first text rule that matches. ok is
// false when the question stays for the lead.
func (e *env) chooseAnswer(entry session.Entry, screen Screen, facts agent.PaneFacts) (answerChoice, bool, error) {
	chosen := answerChoice{
		action:   policy.Action{Kind: "answer", Role: entry.Role},
		decision: policy.Decision{Kind: policy.Allow, Reason: "configured peer answer", Rule: "rex.auto_answers"},
	}
	if ask := facts.CommandQuestion; ask != "" && strings.Contains(screen.Text, ask) {
		// commandChoice reads only the block right above the options, so an
		// older prompt in the scrollback gives no command and leaves the
		// question to the lead. A trust screen is never the gate's.
		return e.commandChoice(entry, screen, facts, ask)
	}
	if corescreen.InstallQuestion(screen) {
		return chosen, false, nil
	}
	for _, rule := range e.cfg.Rex.AutoAnswers {
		if strings.Contains(screen.Question, rule.Match) {
			chosen.label = rule.Answer
			return chosen, config.SafeAutoAnswer(rule.Answer) && rule.Answer != "", nil
		}
	}
	return chosen, false, nil
}

// commandChoice asks the gate about the command of a prompt. An undecided
// verdict is logged as a request, once per question, and left for the lead.
func (e *env) commandChoice(entry session.Entry, screen Screen, facts agent.PaneFacts, ask string) (answerChoice, bool, error) {
	command := corescreen.PromptCommand(screen.Text, ask)
	if command == "" {
		return answerChoice{}, false, nil
	}
	lead, err := e.lead(time.Now())
	if err != nil {
		return answerChoice{}, false, err
	}
	rules := policy.LeadRules{}
	if lead.target != nil {
		if rules, err = gate.ImportLead(e.root, lead.target.Agent); err != nil {
			return answerChoice{}, false, err
		}
	}
	rules.RoleSandbox = "read-only"
	if role, ok := e.cfg.Roles[entry.Role]; ok {
		rules.RoleSandbox = role.Sandbox
	}
	action := policy.Action{Kind: "exec", Target: stripAssignments(command), Role: entry.Role, Work: e.root, Cwd: e.root}
	decision := policy.Evaluate(e.cfg.Policy, rules, mode.Lower(lead.mode, entry.Mode), action)
	chosen := answerChoice{action: action, decision: decision}
	switch decision.Kind {
	case policy.Allow:
		chosen.label = answerFor(facts, "allow")
	case policy.Deny:
		chosen.label = answerFor(facts, "deny")
	default:
		if entry.Asked != screen.Question {
			if err := gate.LogRequest(&store.Store{Dir: e.dir}, action, decision); err != nil {
				return chosen, false, err
			}
		}
		return chosen, false, nil
	}
	return chosen, chosen.label != "", nil
}

// optionFor is the index of the one option that choice names, or -1. A label
// or its parenthetical/key hint counts, never the fuzzy substring or number
// matching available to the lead; two matches count as none.
func optionFor(screen Screen, choice string) int {
	target := -1
	for i, option := range screen.Options {
		if option.Label == choice || strings.HasPrefix(option.Label, choice+" (") || choice == "No" && strings.HasPrefix(option.Label, "No, ") {
			if target >= 0 {
				return -1
			}
			target = i
		}
	}
	return target
}

// answerFor is the label the pane picks for a gate verdict, "allow" or "deny".
func answerFor(f agent.PaneFacts, verdict string) string {
	for _, a := range f.Answers {
		if a.Verdict == verdict {
			return a.Label
		}
	}
	return ""
}

// Strip only literal assignments. Shell expansions stay in the command so
// the gate asks about syntax it cannot safely judge.
var assignment = regexp.MustCompile("^[A-Za-z_][A-Za-z0-9_]*=(?:[A-Za-z0-9_./:@%+,=-]*|'[^'\\r\\n]*'|\"[^\"$`\\\\\\r\\n]*\")(?:[ \\t]+|$)")

func stripAssignments(command string) string {
	for {
		prefix := assignment.FindString(command)
		if prefix == "" {
			return command
		}
		command = strings.TrimSpace(strings.TrimPrefix(command, prefix))
		if command == "" {
			return command
		}
	}
}
