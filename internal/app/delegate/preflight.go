package delegate

import (
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
// worker of the role to run some commands of the task's Check section. No
// worker started. Blocked lists them in order; Skipped holds the Check lines
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
		if b.Reason != "" {
			part += " (" + b.Reason + ")"
		}
		if b.Decision == policy.Ask {
			part += "; the person may approve it with " + RememberHint(b.Command)
		}
		parts = append(parts, part)
	}
	msg := fmt.Sprintf("preflight: no worker started; the gate would not allow %d Check command(s) for role %s: %s", len(e.Blocked), e.Role, strings.Join(parts, " | "))
	if len(e.Skipped) > 0 {
		msg += "; not read as commands and skipped: " + quotedList(e.Skipped)
	}
	return msg + "; change the Check section, or pass --skip-preflight to start anyway"
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
// Write folders the small-task rule needs, and the Check commands the
// preflight judges for each route.
type taskPlan struct {
	folders  []string
	off      bool // a replay, a judge, a calibration cell or --skip-preflight
	commands []string
	unread   []string
	// blocked is the preflight refusal of a route that was left for the next.
	blocked error
}

// readTaskPlan is readTask with the taskPlan of the task. A replay, a judge and
// a calibration cell are not the lead's hand-over: their Check is not read.
func readTaskPlan(root, path string, opts Options) (writes, reads []string, original []byte, plan *taskPlan, err error) {
	if writes, reads, original, err = readTask(root, path, opts.Stderr); err != nil {
		return nil, nil, nil, nil, err
	}
	plan = &taskPlan{folders: readWriteFolders(root, path)}
	plan.off = opts.SkipPreflight || opts.Calibration || opts.ReplayOf != "" || opts.Judge
	if !plan.off {
		if plan.commands, plan.unread, err = readChecks(root, path); err != nil {
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

// check judges the Check commands for the worker of route. A write role whose
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
	if err = preflight(r.cfg, r.rules, m, r.role, r.root, r.state, r.protected, r.reads, p.commands, p.unread, r.stderr); err == nil {
		out.SkippedChecks = p.unread
		return false, nil
	}
	if attempt > 0 || r.pinned {
		return false, err
	}
	p.blocked = err
	return true, nil
}

// readChecks reads the commands of the Check section of the task at path, and
// the lines it could not read as a command.
func readChecks(root, path string) (commands, skipped []string, err error) {
	work := WorkTree(root)
	name, _, err := work.Resolve(path)
	if err != nil {
		return nil, nil, err
	}
	return taskspec.CheckCommands(work.FS, name)
}

// preflight judges the Check commands of a task with the gate's decision code,
// for a write role of this mode in dir, and returns a PreflightError for those
// the gate would ask about or deny. It only reports. Skipped lines that do not
// block are named on stderr. A read-only role runs none of them. reads are the
// task's Read entries the worker's gate gets.
func preflight(cfg *config.Config, rules policy.LeadRules, m mode.Mode, role, work, state string, protected, reads, commands, skipped []string, stderr io.Writer) error {
	rules.RoleSandbox = "read-only"
	if r, ok := cfg.Roles[role]; ok {
		rules.RoleSandbox = r.Sandbox
	}
	// The worker's Bash tool calls reach the gate with no shell name; only the
	// PowerShell hook tool sets "powershell".
	blocked := policy.Preflight(policy.PreflightInput{Policy: cfg.Policy, Lead: rules, Mode: m, Role: role, Work: work, State: state, ProtectedRoots: protected, Reads: reads, Shell: ""}, commands)
	if len(blocked) > 0 {
		return &PreflightError{Role: role, Blocked: blocked, Skipped: skipped}
	}
	if len(skipped) > 0 && stderr != nil {
		_, _ = fmt.Fprintf(stderr, "duolog delegate: preflight skipped Check lines that are not a command: %s\n", quotedList(skipped))
	}
	return nil
}
