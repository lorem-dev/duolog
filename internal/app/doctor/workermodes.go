package doctor

import (
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/trust"
	"github.com/lorem-dev/duolog/internal/trusthome"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// gateBinary is a variable so tests can fail the worker PATH lookup.
var gateBinary = workerenv.GateBinary

// workerModeFindings reports the worker_modes entries: one info line per agent
// value, a warning for each looser value whose gate cannot be shown to run,
// and each value a project layer tried to raise.
func workerModeFindings(cfg *config.Config, root string) []Finding {
	var out []Finding
	wm := cfg.WorkerModes
	for _, kind := range []string{"role", "family"} {
		entries := wm.Roles
		if kind == "family" {
			entries = wm.Families
		}
		names := make([]string, 0, len(entries))
		for name := range entries {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			e := entries[name]
			for _, agent := range []string{"claude", "codex"} {
				native, extras, looser, applies := describeWorkerMode(agent, e)
				if native == "" {
					continue
				}
				rank, _ := mode.Rank(agent, native)
				when := "it applies " + applies
				switch {
				case kind == "role" && cfg.Roles[name].Sandbox == "read-only" && rank > 0:
					// mode.Resolve gives a read-only role plan or read-only.
					when, looser = "it never applies: role "+name+" is read-only, so its workers start in plan", false
				case kind == "family" && rank > 2:
					// WorkerModes.Request: a family entry only lowers, and
					// alone one above the default is skipped.
					when, looser = "a family entry only lowers: it lowers a role entry above it, and alone it changes nothing", false
				}
				ask := fmt.Sprintf("worker_modes: %s %s asks %s %s", kind, name, agent, native+extras)
				out = append(out, Finding{"info", ask + "; " + when})
				if !looser {
					continue
				}
				if problem := gateProblem(cfg, agent, root); problem != "" {
					out = append(out, Finding{"warning", fmt.Sprintf("%s, but the gate cannot be shown to run for %s here: %s; delegate refuses such a run", ask, agent, problem)})
				}
			}
		}
	}
	for _, line := range wm.Ignored {
		out = append(out, Finding{"warning", line})
	}
	return out
}

// describeWorkerMode names the native value an entry asks for one agent, its
// Codex extras, whether it is looser than duolog's default flags, and the
// lead modes at which it applies. native is empty when the entry asks nothing
// for the agent.
func describeWorkerMode(agent string, e config.WorkerMode) (native, extras string, looser bool, applies string) {
	native = e.For(agent)
	if agent == "codex" {
		if e.Network || len(e.WritableRoots) > 0 {
			if native == "" {
				native = "workspace-write"
			}
			var parts []string
			if e.Network {
				parts = append(parts, "network")
			}
			if n := len(e.WritableRoots); n == 1 {
				parts = append(parts, "1 writable root")
			} else if n > 1 {
				parts = append(parts, fmt.Sprintf("%d writable roots", n))
			}
			extras = " with " + parts[0]
			if len(parts) == 2 {
				extras += " and " + parts[1]
			}
			return native, extras, true, "when the lead is in edits or full"
		}
	}
	switch rank, _ := mode.Rank(agent, native); {
	case native == "":
	case rank == 0:
		applies = "always"
	case rank == 1 || agent == "codex":
		applies = "when the lead is in ask or above"
	case native == "acceptEdits":
		applies = "when the lead is in edits or full"
	case native == "auto":
		applies, looser = "when the lead is in auto or full", true
	default:
		applies, looser = "when the lead is in full", true
	}
	return native, extras, looser, applies
}

// gateProblem says why the gate cannot be shown to run for a worker of agent
// in root, or "" when it can. It reads what delegate's gate preflight reads:
// the hooks, folder trust and gate binary of the environment a worker gets,
// env_passthrough applied, in each variant that may run, never duolog's own.
func gateProblem(cfg *config.Config, agent, root string) string {
	variants := []workerenv.Variant{workerenv.Free}
	if cfg.PaidRuns.Agent(agent).Allow {
		variants = append(variants, workerenv.Paid)
	}
	for _, variant := range variants {
		p, err := workerenv.Build(workerenv.ProcessInput{Agent: agent, Shape: workerenv.Worker, Variant: variant, Config: cfg, Environ: os.Environ()})
		if err != nil {
			return err.Error()
		}
		if problem := envGateProblem(agent, root, p.Env); problem != "" {
			if variant == workerenv.Paid {
				problem = "in the paid variant: " + problem
			}
			return problem
		}
	}
	return ""
}

// envGateProblem is gateProblem for the worker environment env.
func envGateProblem(agent, root string, env []string) string {
	state, err := trust.HooksEnv(agent, root, env)
	if err != nil {
		return workerEnvError(err)
	}
	folder, err := trust.FolderEnv(agent, root, env)
	if err != nil {
		return workerEnvError(err)
	}
	if problem := trust.GateProblem(agent, state, folder); problem != "" {
		return problem
	}
	if _, err := gateBinary("PATH=" + trusthome.Getenv(env, "PATH")); err != nil {
		return err.Error()
	}
	return ""
}

// workerEnvError words a read error, and names the passthrough when the
// worker's environment has no home folder.
func workerEnvError(err error) string {
	if errors.Is(err, trusthome.ErrNoHome) {
		return err.Error() + " for the worker, so the settings its agent loads cannot be read; add it to env_passthrough"
	}
	return err.Error()
}
