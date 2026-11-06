package peer

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/lorem-dev/duolog/internal/app/launch"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/mode"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
)

// agentArgv is the interactive command of a peer: the admitted process's
// launch argv once, its prefix with its settings or global overrides and its
// permission words (paneLaunch), then the pane's own flags, none of which
// grants a permission. Updates are off through the process: Codex's setting
// in its prefix, Claude's variable in its environment. Nothing here bypasses
// the sandbox; Claude's protected roots are in its settings, and Codex reads
// have only the gate. m is the lead's mode the pane opened under.
func agentArgv(route coreroute.Route, m mode.Mode, p corelaunch.Process) []string {
	switch route.Agent {
	case "codex":
		args := p.LaunchArgv("-m", route.Model)
		if route.Effort != "" {
			args = append(args, "-c", `model_reasoning_effort="`+route.Effort+`"`)
		}
		args = append(args, "-c", "tui.notifications=false", "-c", "notify=[]")
		if route.Context == "minimal" {
			args = append(args, "-c", "project_doc_max_bytes=0")
		}
		return args
	case "claude":
		args := p.LaunchArgv("--model", route.Model)
		if route.Effort != "" {
			args = append(args, "--effort", route.Effort)
		}
		// A peer cannot delegate: the gate denies a spawn, and without the tools the
		// peer does not spend turns trying. One value with =, as the option takes a list.
		disallowed := []string{"Agent", "Task"}
		// A peer that only the gate keeps read-only starts without the built-in
		// tools that are not read-only either, a second wall behind the gate.
		if mode.GateHeld(route.Agent, m, route.Sandbox) {
			disallowed = append(disallowed, mode.HeldDisallowedTools(true)...)
		}
		return append(args, "--disallowedTools="+strings.Join(disallowed, ","))
	}
	return nil
}

// paneLaunch is what a pane's process carries after its prefix, in
// corelaunch.ProcessInput.Launch: the native permission flags the lead's mode
// m picks. A Codex workspace-write peer gets no network and no writable root
// beyond the project, whatever Codex's own config files say, as a worker
// does.
func paneLaunch(route coreroute.Route, m mode.Mode) []string {
	return mode.PinCodexSandbox(route.Agent, mode.Args(route.Agent, m, route.Sandbox), false, nil)
}

// agentEnv builds a peer pane's process: its environment, its argv prefix and
// its settings, from one launch description, as delegate's workerEnv does for
// a worker. The env file and the argv both come from it. REX_ names never pass
// on: a worker that learns the session could rename or rearrange the lead's
// terminal. DUOLOG_PEER names the pane's entry, which the gate puts on the
// worker's asks.
func agentEnv(build launch.Build, in corelaunch.ProcessInput) (corelaunch.Process, error) {
	in.Shape = corelaunch.Peer
	return build(in)
}

// peerInput describes the pane of route in variant under a lead in mode m:
// environ, the route's context, the state folder and protected roots its
// Claude settings deny, Claude's notifications off, and the permission words
// the pane launches with, so that the gate admits them. run, when set, is the pane's DUOLOG_RUN:
// the run id duolog drew for this launch, never a vendor id; the gate's
// telemetry events and the peer's own carry it.
func peerInput(cfg *config.Config, route coreroute.Route, m mode.Mode, variant corelaunch.Variant, leadID, role, peerID, run, stateDir string,
	roots, environ []string) corelaunch.ProcessInput {
	in := corelaunch.ProcessInput{Agent: route.Agent, Shape: corelaunch.Peer, Variant: variant, Config: cfg,
		Environ: environ, LeadID: leadID, Role: role, PeerID: peerID, StateDir: stateDir,
		Minimal: route.Context == "minimal", ProtectedRoots: roots, Launch: paneLaunch(route, m)}
	if run != "" {
		in.ExtraEnv = []string{"DUOLOG_RUN=" + run}
	}
	// A pane opened under a lead in plan stays at level plan when the lead
	// later moves to a looser mode: the gate takes DUOLOG_MODE only to lower.
	// A read-only role needs none; its role sandbox holds it.
	if m == mode.Plan {
		in.ExtraEnv = append(in.ExtraEnv, "DUOLOG_MODE="+string(mode.Plan))
	}
	if route.Agent == "claude" {
		in.ExtraSettings = map[string]any{"preferredNotifChannel": "notifications_disabled"}
	}
	return in
}

// writeEnvFile stores the pane's variables for duolog peer launch, which deletes
// the file as it reads it. They do not go on the rex command line: that holds API
// keys, and any local user can read a command line.
func writeEnvFile(dir string, pairs []string) (string, error) {
	data, err := json.Marshal(pairs)
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, ".peer-env-*.json")
	if err != nil {
		return "", err
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		_ = os.Remove(f.Name())
		return "", writeErr
	}
	return f.Name(), nil
}

// launchArgv starts a program through duolog, which gives it exactly the listed
// variables. Rex starts it directly (--shell=none).
func launchArgv(exe, envFile string, program []string) []string {
	return append([]string{exe, "peer", "launch", "--env-file", envFile, "--"}, program...)
}
