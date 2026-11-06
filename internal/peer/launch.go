package peer

import (
	"encoding/json"
	"os"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/router"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// agentArgv is the interactive command of a peer: the built process's prefix
// once, with its settings or global overrides, then the pane's own flags. The
// lead's mode picks the native permission flags. Updates are off through the
// process: Codex's setting in its prefix, Claude's variable in its
// environment. Nothing here bypasses the sandbox; Claude's protected roots
// are in its settings, and Codex reads have only the gate.
func agentArgv(route router.Route, m mode.Mode, p workerenv.Process) []string {
	switch route.Agent {
	case "codex":
		args := p.Argv("-m", route.Model)
		args = append(args, mode.Args("codex", m, route.Sandbox)...)
		if route.Effort != "" {
			args = append(args, "-c", `model_reasoning_effort="`+route.Effort+`"`)
		}
		args = append(args, "-c", "tui.notifications=false", "-c", "notify=[]")
		if route.Context == "minimal" {
			args = append(args, "-c", "project_doc_max_bytes=0")
		}
		return args
	case "claude":
		args := p.Argv("--model", route.Model)
		if route.Effort != "" {
			args = append(args, "--effort", route.Effort)
		}
		// A peer cannot delegate: the gate denies a spawn, and without the tools the
		// peer does not spend turns trying. One value with =, as the option takes a list.
		args = append(args, "--disallowedTools=Agent,Task")
		return append(args, mode.Args("claude", m, route.Sandbox)...)
	}
	return nil
}

// agentEnv builds a peer pane's process: its environment, its argv prefix and
// its settings, from one launch description, as delegate's workerEnv does for
// a worker. The env file and the argv both come from it. REX_ names never pass
// on: a worker that learns the session could rename or rearrange the lead's
// terminal. DUOLOG_PEER names the pane's entry, which the gate puts on the
// worker's asks.
func agentEnv(in workerenv.ProcessInput) (workerenv.Process, error) {
	in.Shape = workerenv.Peer
	return workerenv.Build(in)
}

// peerInput describes the pane of route in variant: environ, the route's
// context, the state folder and protected roots its Claude settings deny,
// and Claude's notifications off.
func peerInput(cfg *config.Config, route router.Route, variant workerenv.Variant, leadID, role, peerID, stateDir string,
	roots, environ []string) workerenv.ProcessInput {
	in := workerenv.ProcessInput{Agent: route.Agent, Shape: workerenv.Peer, Variant: variant, Config: cfg,
		Environ: environ, LeadID: leadID, Role: role, PeerID: peerID, StateDir: stateDir,
		Minimal: route.Context == "minimal", ProtectedRoots: roots}
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
