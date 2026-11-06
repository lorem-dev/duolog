package peer

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
)

const commandQuestion = "Would you like to run the following command?"

// autoAnswer gives command prompts to the gate and declines Claude's plugin
// offers before considering text rules. A missing command or option leaves the
// question for the lead, and a plugin install question never reaches text rules.
func (e *env) autoAnswer(ctx context.Context, entry session.Entry, screen Screen) (bool, error) {
	if entry.Agent == "claude" {
		if offer, ok := readPluginOffer(screen); ok {
			return e.declinePlugin(ctx, entry, screen, offer)
		}
		if installQuestion(screen) {
			return e.declinePlugin(ctx, entry, screen, pluginOffer{})
		}
	}
	// A billing choice, such as using an API key the agent found, is the
	// lead's to answer, whatever auto_answers match.
	if billingQuestion(screen) {
		return false, nil
	}
	choice := ""
	action := gate.Action{Kind: "answer", Role: entry.Role}
	decision := gate.Decision{Kind: gate.Allow, Reason: "configured peer answer", Rule: "rex.auto_answers"}
	if entry.Agent == "codex" && strings.Contains(screen.Text, commandQuestion) {
		command := promptCommand(screen.Text)
		if command == "" {
			return false, nil
		}
		lead, err := e.lead(time.Now())
		if err != nil {
			return false, err
		}
		rules := gate.LeadRules{}
		if lead.target != nil {
			rules, err = gate.ImportLead(e.root, lead.target.Agent)
			if err != nil {
				return false, err
			}
		}
		if role, ok := e.cfg.Roles[entry.Role]; ok {
			rules.RoleSandbox = role.Sandbox
		} else {
			rules.RoleSandbox = "read-only"
		}
		action = gate.Action{Kind: "exec", Target: stripAssignments(command), Role: entry.Role, Work: e.root, Cwd: e.root}
		decision = gate.Evaluate(e.cfg.Policy, rules, mode.Lower(lead.mode, entry.Mode), action)
		switch decision.Kind {
		case gate.Allow:
			choice = "Yes, proceed"
		case gate.Deny:
			choice = "No"
		default:
			if entry.Asked != screen.Question {
				if err := gate.LogRequest(&store.Store{Dir: e.dir}, action, decision); err != nil {
					return false, err
				}
			}
			return false, nil
		}
	} else {
		if installQuestion(screen) {
			return false, nil
		}
		for _, rule := range e.cfg.Rex.AutoAnswers {
			if strings.Contains(screen.Question, rule.Match) {
				if !config.SafeAutoAnswer(rule.Answer) {
					return false, nil
				}
				choice = rule.Answer
				break
			}
		}
	}
	if choice == "" {
		return false, nil
	}
	// Automatic selection accepts a label or its parenthetical/key hint, never
	// the fuzzy substring or number matching available to the lead.
	target := -1
	for i, option := range screen.Options {
		if option.Label == choice || strings.HasPrefix(option.Label, choice+" (") || choice == "No" && strings.HasPrefix(option.Label, "No, ") {
			if target >= 0 {
				return false, nil
			}
			target = i
		}
	}
	if target < 0 || !config.SafeAutoAnswer(screen.Options[target].Label) {
		return false, nil
	}
	if err := e.selectOption(ctx, entry, screen, screen.Options[target].Label); err != nil {
		return false, err
	}
	if action.Kind == "answer" {
		action.Target = screen.Question + " => " + screen.Options[target].Label
	}
	if err := gate.Log(&store.Store{Dir: e.dir}, action, decision); err != nil {
		return false, err
	}
	_, err := e.update(entry.ID, func(x *session.Entry) { x.Asked = "" })
	return true, err
}

// promptCommand reads the displayed command, not the bounded question summary.
// Only the command block below the fixed prompt and before the menu counts.
func promptCommand(text string) string {
	_, rest, ok := strings.Cut(text, commandQuestion)
	if !ok {
		return ""
	}
	var command []string
	for _, line := range strings.Split(rest, "\n") {
		line = strings.TrimSpace(line)
		if numberedRe.MatchString(line) {
			break
		}
		if len(command) == 0 {
			if c, ok := strings.CutPrefix(line, "$ "); ok {
				command = append(command, c)
			}
		} else {
			if line == "" {
				break
			}
			command = append(command, line)
		}
	}
	return strings.Join(command, "\n")
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
