package delegate

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	coreagent "github.com/lorem-dev/duolog/internal/core/agent"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/project"
)

// skippedTrust is local to this run, so a folder's refusal never disables an
// agent for another folder or turns into a quota-unavailable record.
type skippedTrust struct{ error }

func (e skippedTrust) Unwrap() error { return e.error }

func workerTrustCheck(d Deps, stateDir, role, root string, env, protected []string, opts Options, route coreroute.Route, wm workerMode) (string, error) {
	t := d.Agents[route.Agent].Trust
	state, folder, err := workerGateState(d, stateDir, route.Agent, root, env, opts)
	gap := trustGap{route.Agent == "codex", t, state, folder, root, stateDir, env}
	noHome := errors.Is(err, coreagent.ErrNoHome)
	if err != nil && !noHome {
		if gap.codex && errors.Is(err, coreagent.ErrWorktreeChanged) {
			return "", skippedTrust{gap.refusal(err)}
		}
		return "", err
	}
	if noHome {
		claude := route.Agent == "claude"
		state, folder = coreagent.TrustState{Trusted: claude}, claude
		gap.state, gap.folder = state, folder
	}
	var warning string
	if noHome && wm.needsPreflight() {
		err = wm.gatePreflight(d, route.Agent, root, env, protected)
	} else {
		warning, err = checkWorkerTrust(d, role, root, env, protected, opts, route, wm, state, folder)
	}
	if err != nil {
		if noHome {
			err = fmt.Errorf("%w; %s", err, noHomeProblem())
		}
		err = gap.refusal(err)
		if gap.codex && (!state.Trusted || !folder || state.Unproven != "") {
			err = skippedTrust{err}
		}
		return "", err
	}
	if noHome {
		warning += "; " + noHomeProblem()
	}
	return warning, nil
}

func checkWorkerTrust(d Deps, role, root string, env, protected []string, opts Options, route coreroute.Route, wm workerMode, state coreagent.TrustState, folder bool) (string, error) {
	t := d.Agents[route.Agent].Trust
	if opts.Calibration {
		if err := calibrationGate(t, route.Agent, root, env); err != nil {
			return "", err
		}
	}
	warning, err := trustWarning(route, role, root, state, folder)
	if err != nil {
		return "", err
	}
	if wm.needsPreflight() {
		if err := wm.gatePreflight(d, route.Agent, root, env, protected); err != nil {
			return "", err
		}
	}
	if route.Agent == "codex" && route.Sandbox != "read-only" && (!folder || state.Unproven != "") {
		return "", fmt.Errorf("codex trust missing for write role %s in %s: %s", role, root, trustProblem(t, route.Agent, state, folder))
	}
	return warning, nil
}

func trustSkipLine(err error, root string, w io.Writer) string {
	line := fmt.Sprintf("Codex skipped: %v; run codex in %s once, accept folder trust, and approve duolog's hooks in /hooks", err, root)
	if w != nil {
		fmt.Fprintln(w, "duolog: "+line)
	}
	return line
}

// workflowLine keeps lead-owned process requirements from stalling the task
// without relaxing the project's other rules. It is one of workerLines.
const workflowLine = "The lead owns the project's workflow; this task is your plan. A missing workflow skill or process step is no reason to stop. All other project rules still apply, especially security, privacy and limits on what you may change."

// trustGap is what Run knows of a worker's Codex trust when it refuses the
// worker: whether the route's agent is Codex, its trust, the gate state and
// folder trust read for the worker, its folder, the session folder and the
// worker's env.
type trustGap struct {
	codex  bool
	trust  Trust
	state  coreagent.TrustState
	folder bool
	root   string
	dir    string
	env    []string
}

// refusal adds to err, for a Codex worker whose folder or hooks are not
// trusted, the worker folder and, in a linked worktree, why the lead
// checkout's trust was not carried over. Other refusals are returned as they
// are.
func (g trustGap) refusal(err error) error {
	if !g.codex || (g.state.Trusted && g.folder && g.state.Unproven == "") {
		return err
	}
	if !strings.Contains(err.Error(), g.root) {
		err = fmt.Errorf("%w; worker folder: %s", err, g.root)
	}
	diagnostic, ok := g.trust.(interface {
		WorktreeTrustProblem(string, string, []string) string
	})
	if !ok {
		return err
	}
	base, absErr := filepath.Abs(filepath.Dir(g.dir))
	if absErr != nil {
		return err
	}
	if problem := diagnostic.WorktreeTrustProblem(project.LeadRoot(base, g.root), g.root, g.env); problem != "" {
		err = fmt.Errorf("%w; %s", err, problem)
	}
	return err
}
