package delegate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	coreagent "github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/mode"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
)

// workerMode is the native permission mode one delegate worker starts in.
type workerMode struct {
	mode.Resolved
	Request       string // the requested native name, for the line
	Network       bool
	WritableRoots []string
	// Held reports a worker that only duolog's gate keeps read-only
	// (mode.GateHeld): Claude at level plan or in a read-only role, which
	// starts in Claude's default mode. It needs its gate shown to run, though
	// nothing it asks is looser than duolog's default.
	Held bool
	// LeadPlan reports a worker started under a lead in plan. Its level stays
	// plan when the lead later moves to a looser mode (env), as a peer's does.
	LeadPlan bool
}

// resolveWorkerMode resolves the worker_modes entries for one route. It reads
// the family from route.Model, so it runs before resolveModel replaces the
// alias. Codex extras come only from the role's own entry, whichever entry
// named the native mode, when the resolved sandbox is workspace-write at
// level edits or full.
func resolveWorkerMode(cfg *config.Config, role string, route coreroute.Route, lead session.Lead, effective mode.Mode) workerMode {
	req := cfg.WorkerModes.Request(role, config.RouteFamilies(cfg, role, route.Model, route.Sandbox), route.Agent)
	ceiling := mode.NewCeiling(lead.Agent, lead.Native, lead.Mode, effective)
	w := workerMode{Resolved: mode.Resolve(route.Agent, req, ceiling, route.Sandbox), Request: req.Native}
	w.Held = mode.GateHeld(route.Agent, w.Level, route.Sandbox)
	w.LeadPlan = effective == mode.Plan && w.Level == mode.Plan
	if route.Agent == "codex" && w.Native == "workspace-write" && (w.Level == mode.Edits || w.Level == mode.Full) && route.Sandbox == "workspace-write" {
		w.Network, w.WritableRoots = cfg.WorkerModes.Extras(role)
		if (w.Network || len(w.WritableRoots) > 0) && w.Source == "default" {
			w.Source = "role " + role
		}
	}
	return w
}

// applies reports whether an entry decided anything for this worker.
func (w workerMode) applies() bool {
	return w.Source != "default" && w.Source != ""
}

// env is what the worker's gate reads: DUOLOG_MODE when an entry applies or
// the worker starts under a lead in plan (LeadPlan), and DUOLOG_NATIVE always,
// for the decision log. Otherwise the gate keeps the lead's live mode as
// before; a read-only role's sandbox holds such a worker to reads.
func (w workerMode) env() []string {
	env := []string{"DUOLOG_NATIVE=" + w.Native}
	if w.applies() || w.LeadPlan {
		env = append(env, "DUOLOG_MODE="+string(w.Level))
	}
	return env
}

// line tells the lead which native mode an entry gave the worker; empty when
// no entry applies.
func (w workerMode) line() string {
	if !w.applies() {
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

// needsPreflight reports a worker that may start only after gatePreflight
// shows its gate runs: a looser one (needsGate), or one that only the gate
// keeps read-only (Held).
func (w workerMode) needsPreflight() bool {
	return w.needsGate() || w.Held
}

// gateState reads the hook state and folder trust of an agent process whose
// environment is env: the settings, plugins, approvals and trusted folders
// that environment's CLAUDE_CONFIG_DIR or CODEX_HOME select.
//
// An agent with no Trust reads as untrusted: no hooks approved, no folder
// trusted, so a write role does not start and a read-only role runs.
func gateState(t Trust, root string, env []string) (coreagent.TrustState, bool, error) {
	if t == nil {
		return coreagent.TrustState{}, false, nil
	}
	state, err := t.Hooks(root, env)
	if err != nil {
		return coreagent.TrustState{}, false, err
	}
	folder, err := t.Folder(root, env)
	return state, folder, err
}

// trustProblem says why the gate of agent cannot be shown to run from its
// trust state; an agent with no Trust cannot be shown to run it at all.
func trustProblem(t Trust, agent string, state coreagent.TrustState, folder bool) string {
	if t == nil {
		return "the hook trust of " + agent + " cannot be read, so its gate cannot be shown to run"
	}
	return t.GateProblem(state, folder)
}

// gatePreflight refuses a looser or held worker (needsPreflight) when the gate cannot be shown to run
// for the process whose environment is env, or when a writable root overlaps
// a protected folder. Hooks, folder trust, the gate binary on PATH and the
// protected agent folders all come from env, the environment that process
// runs with, never from duolog's own.
func (w workerMode) gatePreflight(d Deps, agent, root string, env, protected []string) error {
	t := d.Agents[agent].Trust
	state, folder, err := gateState(t, root, env)
	var problem string
	switch {
	case errors.Is(err, coreagent.ErrNoHome):
		// The agent falls back to a home env does not name, so its settings
		// cannot be read and its gate cannot be shown.
		problem = noHomeProblem()
	case err != nil:
		return err
	default:
		problem = trustProblem(t, agent, state, folder)
	}
	pathEnv := "PATH=" + corelaunch.Getenv(env, "PATH")
	gate, err := d.GateBinary(pathEnv)
	if problem == "" && err != nil {
		problem = err.Error()
	}
	// Only the gate keeps a held worker read-only, so its hook must be the
	// gate itself, not a hook that names it.
	if problem == "" && w.Held && t != nil {
		problem = t.HeldGateProblem(root, env, gate)
	}
	if problem == "" && len(w.WritableRoots) > 0 {
		protected = append(slices.Clone(protected), programFolders(pathEnv, gate)...)
		problem = protectedRoot(d.Canonical, w.WritableRoots, protected, env)
	}
	if problem == "" {
		return nil
	}
	if w.Held && !w.needsGate() {
		return fmt.Errorf("gate preflight failed for %s at level plan (native %s; only the duolog gate keeps it read-only): %s; nothing ran", agent, w.Native, problem)
	}
	return fmt.Errorf("gate preflight failed for %s %s (%s): %s; nothing ran", agent, w.Native, w.Source, problem)
}

// HeldGate refuses a peer pane that only the gate keeps read-only
// (mode.GateHeld) when its gate cannot be shown to run for the process whose
// environment is env, as gatePreflight refuses such a worker.
func HeldGate(d Deps, agent, root string, env []string) error {
	w := workerMode{Resolved: mode.Resolved{Level: mode.Plan, Native: mode.Default(agent, mode.Plan, "read-only")}, Held: true}
	return w.gatePreflight(d, agent, root, env, nil)
}

// noHomeProblem says why nothing a worker's agent loads can be read from an
// environment without a home folder.
func noHomeProblem() string {
	name := "HOME"
	if runtime.GOOS == "windows" {
		name = "USERPROFILE"
	}
	return "the worker's environment has no home folder (" + name + "), so the settings its agent loads cannot be read; add " + name + " to env_passthrough"
}

// calibrationGate refuses a calibration cell or rewrite whose gate cannot be
// shown to run for the process whose environment is env, whatever its worker
// mode: calibration promises that no cell runs without the gate.
func calibrationGate(t Trust, agent, root string, env []string) error {
	state, folder, err := gateState(t, root, env)
	var problem string
	switch {
	case errors.Is(err, coreagent.ErrNoHome):
		problem = noHomeProblem()
	case err != nil:
		return err
	default:
		problem = trustProblem(t, agent, state, folder)
	}
	if problem == "" {
		return nil
	}
	return fmt.Errorf("calibration gate check failed for %s in %s: %s; no cell runs without the duolog gate, and nothing ran", agent, root, problem)
}

// protectedRoot names the first writable root that config refuses now, at
// launch, or that equals, lies inside or holds a protected folder; empty when
// none does. Paths compare canonically (canonical is kit.Canonical). env is
// the worker's environment, whose agent folders are protected too.
func protectedRoot(canonical func(string) string, roots, protected, env []string) string {
	for _, root := range roots {
		if reason := configfile.RefuseRootEnv(root, env); reason != "" {
			return "writable root " + root + ": " + reason
		}
		for _, p := range protected {
			if p != "" && overlap(canonical, p, root) {
				return "writable root " + root + " overlaps protected " + p
			}
		}
	}
	return ""
}

// overlap reports whether one of two local folders is the other or lies inside
// it, compared by canonical.
func overlap(canonical func(string) string, a, b string) bool {
	ca, cb := canonical(a), canonical(b)
	return within(ca, cb) || within(cb, ca)
}

// within reports whether child is parent or lies inside it, comparing the
// spellings as given.
func within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// programFolders lists the folders a writable root must not reach, so that a
// worker's command cannot replace a program a later hook or the lead runs:
// the folders on the worker PATH that hold duolog, claude or codex, and the
// folders those resolve to.
func programFolders(pathEnv, gate string) []string {
	var out []string
	add := func(file string) {
		out = append(out, filepath.Dir(file))
		if resolved, err := filepath.EvalSymlinks(file); err == nil {
			out = append(out, filepath.Dir(resolved))
		}
	}
	if gate != "" {
		add(gate)
	}
	suffixes := []string{""}
	if runtime.GOOS == "windows" {
		suffixes = []string{".exe", ".cmd", ".bat", ".ps1"}
	}
	for _, dir := range filepath.SplitList(strings.TrimPrefix(pathEnv, "PATH=")) {
		if !filepath.IsAbs(dir) {
			continue
		}
		for _, name := range []string{"duolog", "claude", "codex"} {
			for _, suffix := range suffixes {
				if info, err := os.Stat(filepath.Join(dir, name+suffix)); err == nil && !info.IsDir() {
					add(filepath.Join(dir, name+suffix))
				}
			}
		}
	}
	return out
}

// gateLogSize is the size of the gate log, 0 when there is none.
func gateLogSize(stateDir string) int64 {
	if info, err := os.Stat(filepath.Join(stateDir, "decisions.ndjson")); err == nil {
		return info.Size()
	}
	return 0
}
