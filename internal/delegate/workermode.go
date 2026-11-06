package delegate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/pathutil"
	"github.com/lorem-dev/duolog/internal/router"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/trust"
	"github.com/lorem-dev/duolog/internal/trusthome"
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
// alias. Codex extras come only from the role's own entry, whichever entry
// named the native mode, when the resolved sandbox is workspace-write at
// level edits or full.
func resolveWorkerMode(cfg *config.Config, role string, route router.Route, lead session.Lead, effective mode.Mode) workerMode {
	req := cfg.WorkerModes.Request(role, config.RouteFamilies(cfg, role, route.Model, route.Sandbox), route.Agent)
	ceiling := mode.NewCeiling(lead.Agent, lead.Native, lead.Mode, effective)
	w := workerMode{Resolved: mode.Resolve(route.Agent, req, ceiling, route.Sandbox), Request: req.Native}
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

// env is what the worker's gate reads: DUOLOG_MODE only when an entry
// applies, so that without one the gate keeps the lead's mode as before, and
// DUOLOG_NATIVE always, for the decision log.
func (w workerMode) env() []string {
	env := []string{"DUOLOG_NATIVE=" + w.Native}
	if w.applies() {
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

// gateState reads the hook state and folder trust of an agent process whose
// environment is env: the settings, plugins, approvals and trusted folders
// that environment's CLAUDE_CONFIG_DIR or CODEX_HOME select.
func gateState(agent, root string, env []string) (trust.State, bool, error) {
	state, err := trust.HooksEnv(agent, root, env)
	if err != nil {
		return trust.State{}, false, err
	}
	folder, err := trust.FolderEnv(agent, root, env)
	return state, folder, err
}

// gatePreflight refuses a looser worker when the gate cannot be shown to run
// for the process whose environment is env, or when a writable root overlaps
// a protected folder. Hooks, folder trust, the gate binary on PATH and the
// protected agent folders all come from env, the environment that process
// runs with, never from duolog's own.
func (w workerMode) gatePreflight(agent, root string, env, protected []string) error {
	state, folder, err := gateState(agent, root, env)
	problem := ""
	switch {
	case errors.Is(err, trusthome.ErrNoHome):
		// The agent falls back to a home env does not name, so its settings
		// cannot be read and its gate cannot be shown.
		problem = noHomeProblem()
	case err != nil:
		return err
	default:
		problem = trust.GateProblem(agent, state, folder)
	}
	pathEnv := "PATH=" + trusthome.Getenv(env, "PATH")
	gate, err := gateBinary(pathEnv)
	if problem == "" && err != nil {
		problem = err.Error()
	}
	if problem == "" && len(w.WritableRoots) > 0 {
		protected = append(slices.Clone(protected), programFolders(pathEnv, gate)...)
		problem = protectedRoot(w.WritableRoots, protected, env)
	}
	if problem == "" {
		return nil
	}
	return fmt.Errorf("gate preflight failed for %s %s (%s): %s; nothing ran", agent, w.Native, w.Source, problem)
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
func calibrationGate(agent, root string, env []string) error {
	state, folder, err := gateState(agent, root, env)
	problem := ""
	switch {
	case errors.Is(err, trusthome.ErrNoHome):
		problem = noHomeProblem()
	case err != nil:
		return err
	default:
		problem = trust.GateProblem(agent, state, folder)
	}
	if problem == "" {
		return nil
	}
	return fmt.Errorf("calibration gate check failed for %s in %s: %s; no cell runs without the duolog gate, and nothing ran", agent, root, problem)
}

// protectedRoot names the first writable root that config refuses now, at
// launch, or that equals, lies inside or holds a protected folder; empty when
// none does. Paths compare canonically (pathutil.Canonical). env is the
// worker's environment, whose agent folders are protected too.
func protectedRoot(roots, protected, env []string) string {
	for _, root := range roots {
		if reason := config.RefuseRootEnv(root, env); reason != "" {
			return "writable root " + root + ": " + reason
		}
		for _, p := range protected {
			if p != "" && pathutil.Overlap(p, root) {
				return "writable root " + root + " overlaps protected " + p
			}
		}
	}
	return ""
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
