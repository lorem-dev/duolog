package delegate

import (
	"fmt"
	"path/filepath"
	"strings"

	coreagent "github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/project"
)

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
