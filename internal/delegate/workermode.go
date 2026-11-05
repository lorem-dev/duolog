package delegate

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/pathutil"
	"github.com/lorem-dev/duolog/internal/router"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/trust"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// workerMode is the native permission mode one delegate worker starts in.
type workerMode struct {
	mode.Resolved
	Request       string // the requested native name, for the line
	Network       bool
	WritableRoots []string
}

// gateBinary finds the duolog a worker's hooks run; tests replace it.
var gateBinary = workerenv.GateBinary

// resolveWorkerMode resolves the worker_modes entries for one route. It reads
// the family from route.Model, so it runs before resolveModel replaces the
// alias. Codex extras come only from the role's own entry, on workspace-write
// at level edits or full.
func resolveWorkerMode(cfg *config.Config, role string, route router.Route, lead session.Lead, effective mode.Mode) workerMode {
	req := cfg.WorkerModes.Request(role, config.Family(cfg, route.Model), route.Agent)
	ceiling := mode.NewCeiling(lead.Agent, lead.Native, lead.Mode, effective)
	w := workerMode{Resolved: mode.Resolve(route.Agent, req, ceiling, route.Sandbox), Request: req.Native}
	if route.Agent == "codex" && w.Native == "workspace-write" && (w.Level == mode.Edits || w.Level == mode.Full) &&
		route.Sandbox == "workspace-write" && slices.Contains(strings.Split(w.Source, ", "), "role "+role) {
		w.Network, w.WritableRoots = cfg.WorkerModes.Extras(role)
	}
	return w
}

// line tells the lead which native mode an entry gave the worker; empty when
// no entry applies.
func (w workerMode) line() string {
	if w.Source == "default" || w.Source == "" {
		return ""
	}
	native := w.Native
	if w.Network {
		native += "+network"
	}
	if len(w.WritableRoots) > 0 {
		native += fmt.Sprintf("+roots=%d", len(w.WritableRoots))
	}
	if w.LoweredBy == "" {
		return "; native=" + native + " (" + w.Source + ")"
	}
	return "; native=" + native + " (" + w.Source + " asks " + w.Request + "; lowered by " + w.LoweredBy + ")"
}

// needsGate reports a worker that relies on the gate more than duolog's
// default flags do: a looser native name or Codex extras.
func (w workerMode) needsGate() bool {
	return w.Looser || w.Network || len(w.WritableRoots) > 0
}

// gatePreflight refuses a looser worker when the gate cannot be shown to run
// for it, or when a writable root overlaps a protected folder.
func (w workerMode) gatePreflight(agent string, state trust.State, folder bool, protected []string) error {
	problem := trust.GateProblem(agent, state, folder)
	if problem == "" {
		if _, err := gateBinary(workerenv.Path()); err != nil {
			problem = err.Error()
		}
	}
	if problem == "" {
		problem = protectedRoot(w.WritableRoots, protected)
	}
	if problem == "" {
		return nil
	}
	return fmt.Errorf("gate preflight failed for %s %s (%s): %s; nothing ran", agent, w.Native, w.Source, problem)
}

// protectedRoot names the first writable root that equals, lies inside or
// holds a protected folder; empty when none does.
func protectedRoot(roots, protected []string) string {
	for _, root := range roots {
		r := filepath.Clean(pathutil.Resolved(root))
		for _, p := range protected {
			if p == "" {
				continue
			}
			q := filepath.Clean(pathutil.Resolved(p))
			if within(q, r) || within(r, q) {
				return "writable root " + root + " overlaps protected " + p
			}
		}
	}
	return ""
}

// within reports whether child is parent or lies inside it.
func within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// gateLogSize is the size of the gate log, 0 when there is none.
func gateLogSize(stateDir string) int64 {
	if info, err := os.Stat(filepath.Join(stateDir, "decisions.ndjson")); err == nil {
		return info.Size()
	}
	return 0
}
