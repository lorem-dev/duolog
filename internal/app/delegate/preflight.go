package delegate

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/policy"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/taskspec"
)

// PreflightError is a delegation refused because the gate would not allow a
// worker of the role to run some commands of the task's Run and Check sections. No
// worker started. Blocked lists them in order; Skipped holds the section lines
// that were not read as a command and so did not block.
type PreflightError struct {
	Role    string
	Blocked []policy.Blocked
	Skipped []string
}

func (e *PreflightError) Error() string {
	var parts []string
	for _, b := range e.Blocked {
		part := fmt.Sprintf("%s: %s", b.Command, b.Decision)
		if b.Section != "" {
			part = b.Section + ": " + part
		}
		if b.Reason != "" {
			part += " (" + b.Reason + ")"
		}
		if b.Decision == policy.Ask {
			part += "; the person may approve it with " + RememberHint(b.Command)
		}
		parts = append(parts, part)
	}
	msg := fmt.Sprintf("preflight: no worker started; the gate would not allow %d task command(s) for role %s: %s", len(e.Blocked), e.Role, strings.Join(parts, " | "))
	if len(e.Skipped) > 0 {
		msg += "; not read as commands and skipped: " + quotedList(e.Skipped)
	}
	return msg + "; change the Run or Check section, or pass --skip-preflight to start anyway"
}

// RememberHint is the command the person may run to approve command.
func RememberHint(command string) string {
	return "duolog remember '" + strings.ReplaceAll(command, "'", `'\''`) + "'"
}

func quotedList(lines []string) string {
	quoted := make([]string, len(lines))
	for i, line := range lines {
		quoted[i] = fmt.Sprintf("%q", line)
	}
	return strings.Join(quoted, ", ")
}

// taskPlan is what Run reads of a task beyond readTask, before it routes: the
// Write folders the small-task rule needs, and the Run and Check commands the
// preflight judges for each route.
type taskPlan struct {
	folders                []string
	off                    bool // a replay, a judge, a calibration cell or --skip-preflight
	commands               []string
	runCommands, runUnread []string
	unread                 []string
	// blocked is the preflight refusal of a route that was left for the next.
	blocked error
}

// readTaskPlan is readTask with the taskPlan of the task. A replay, a judge and
// a calibration cell are not the lead's hand-over: their commands are not read.
func readTaskPlan(root, path string, opts Options) (writes, reads []string, original []byte, plan *taskPlan, err error) {
	if writes, reads, original, err = readTask(root, path, opts.Stderr); err != nil {
		return nil, nil, nil, nil, err
	}
	plan = &taskPlan{folders: readWriteFolders(root, path)}
	plan.off = opts.SkipPreflight || opts.Calibration || opts.ReplayOf != "" || opts.Judge
	if !plan.off {
		if err = plan.readCommands(root, path); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	return writes, reads, original, plan, nil
}

// refusal is the error that ends a run when no route can start: the
// preflight's refusal of a route left for the next one, else other.
func (p *taskPlan) refusal(other error) error {
	if p.blocked != nil {
		return p.blocked
	}
	return other
}

// inLead refuses a placement in the lead after the preflight blocked a route:
// the lead is not given the task without the refusal.
func (p *taskPlan) inLead(route coreroute.Route) error {
	if (route.Self || route.InLead) && p.blocked != nil {
		return p.blocked
	}
	return nil
}

// preflightRun is what a route's preflight takes from the run.
type preflightRun struct {
	cfg       *config.Config
	rules     policy.LeadRules
	leadMode  mode.Mode
	role      string
	root      string
	state     string
	protected []string
	reads     []string
	stderr    io.Writer
	pinned    bool // a target the person pinned has no other route
}

// check judges the Run and Check commands for the worker of route. A write role whose
// commands the gate would stop does not start: the worker could not run them
// and would ask, or give up. It is judged for this route's worker mode. It
// returns skip for a blocked route that another may replace, and an error for a
// pinned target or a later attempt, which have no other route to try; the
// refusal then stands only when none can run the commands.
func (p *taskPlan) check(r preflightRun, route coreroute.Route, wm workerMode, attempt int, out *Outcome) (skip bool, err error) {
	if route.Sandbox == "read-only" || p.off {
		return false, nil
	}
	m := r.leadMode
	if wm.applies() || wm.LeadPlan {
		m = policy.WorkerMode(r.leadMode, string(wm.Level))
	}
	if err = p.judge(r, m); err == nil {
		out.SkippedChecks = p.skipped()
		return false, nil
	}
	if attempt > 0 || r.pinned {
		return false, err
	}
	p.blocked = err
	return true, nil
}

// preflightSection judges the commands of a task with the gate's decision code,
// for a write role of this mode in dir, and returns a PreflightError for those
// the gate would ask about or deny. It only reports. Skipped lines that do not
// block are named on stderr. A read-only role runs none of them. reads are the
// task's Read entries the worker's gate gets.
func preflightSection(cfg *config.Config, rules policy.LeadRules, m mode.Mode, role, work, state string, protected, reads, commands, skipped []string, stderr io.Writer, section string) error {
	rules.RoleSandbox = "read-only"
	if r, ok := cfg.Roles[role]; ok {
		rules.RoleSandbox = r.Sandbox
	}
	// The worker's Bash tool calls reach the gate with no shell name; only the
	// PowerShell hook tool sets "powershell".
	blocked := policy.Preflight(policy.PreflightInput{Policy: cfg.Policy, Lead: rules, Mode: m, Role: role, Work: work, State: state, ProtectedRoots: protected, Reads: reads, Shell: ""}, commands)
	for i := range blocked {
		blocked[i].Section = section
	}
	if len(blocked) > 0 {
		return &PreflightError{Role: role, Blocked: blocked, Skipped: skipped}
	}
	if len(skipped) > 0 && stderr != nil {
		_, _ = fmt.Fprintf(stderr, "duolog delegate: preflight skipped %s lines that are not a command: %s\n", section, quotedList(skipped))
	}
	return nil
}

func (p *taskPlan) readCommands(root, path string) error {
	work := WorkTree(root)
	name, _, err := work.Resolve(path)
	if err != nil {
		return err
	}
	if p.commands, p.unread, err = taskspec.CheckCommands(work.FS, name); err != nil {
		return err
	}
	p.runCommands, p.runUnread, err = taskspec.RunCommands(work.FS, name)
	return err
}

// skipped lists the Check lines and then the Run lines that were not read as
// commands; a Run line is marked "Run: ".
func (p *taskPlan) skipped() []string {
	out := append([]string(nil), p.unread...)
	for _, line := range p.runUnread {
		out = append(out, "Run: "+line)
	}
	return out
}

// judge uses the same route and mode for both sections and reports all refusals.
func (p *taskPlan) judge(r preflightRun, m mode.Mode) error {
	refusal := &PreflightError{Role: r.role}
	for _, section := range []struct {
		name             string
		commands, unread []string
	}{
		{"Run", p.runCommands, p.runUnread}, {"Check", p.commands, p.unread},
	} {
		err := preflightSection(r.cfg, r.rules, m, r.role, r.root, r.state, r.protected, r.reads, section.commands, section.unread, r.stderr, section.name)
		var blocked *PreflightError
		if errors.As(err, &blocked) {
			refusal.Blocked = append(refusal.Blocked, blocked.Blocked...)
		}
		for _, line := range section.unread {
			refusal.Skipped = append(refusal.Skipped, section.name+": "+line)
		}
	}
	if len(refusal.Blocked) > 0 {
		return refusal
	}
	return nil
}
