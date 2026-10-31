package peer

import (
	"encoding/json"
	"os"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/router"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// agentArgv is the interactive command of a peer. The lead's mode picks the
// native permission flags, and updates are switched off the way task 1 proved:
// a Codex setting and a Claude variable. Nothing here bypasses the sandbox.
func agentArgv(route router.Route, m mode.Mode) []string {
	switch route.Agent {
	case "codex":
		args := []string{"codex", "-m", route.Model}
		args = append(args, mode.Args("codex", m, route.Sandbox)...)
		if route.Effort != "" {
			args = append(args, "-c", `model_reasoning_effort="`+route.Effort+`"`)
		}
		args = append(args, "-c", "check_for_update_on_startup=false")
		if route.Context == "minimal" {
			args = append(args, "-c", "project_doc_max_bytes=0")
		}
		return args
	case "claude":
		args := []string{"claude", "--model", route.Model}
		if route.Effort != "" {
			args = append(args, "--effort", route.Effort)
		}
		return append(args, mode.Args("claude", m, route.Sandbox)...)
	}
	return nil
}

// agentEnv lists NAME=VALUE pairs for the pane. REX_ names never pass on: a
// worker that learns the session could rename or rearrange the lead's terminal.
func agentEnv(cfg *config.Config, agent, leadID string, roles ...string) []string {
	pairs := workerenv.Passthrough(cfg.EnvPassthrough)
	if agent == "claude" {
		pairs = append(pairs, "DISABLE_AUTOUPDATER=1")
	}
	role := ""
	if len(roles) > 0 {
		role = roles[0]
	}
	return append(pairs, workerenv.Path(), "DUOLOG_DEPTH=1", "DUOLOG_LEAD="+leadID, "DUOLOG_ROLE="+role)
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
