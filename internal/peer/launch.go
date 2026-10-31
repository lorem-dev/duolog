package peer

import (
	"os"
	"strings"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/router"
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
	var pairs []string
	for _, name := range cfg.EnvPassthrough {
		if strings.HasPrefix(name, "DUOLOG_") || strings.HasPrefix(name, "REX_") {
			continue
		}
		if value, ok := os.LookupEnv(name); ok {
			pairs = append(pairs, name+"="+value)
		}
	}
	if agent == "claude" {
		pairs = append(pairs, "DISABLE_AUTOUPDATER=1")
	}
	role := ""
	if len(roles) > 0 {
		role = roles[0]
	}
	return append(pairs, "DUOLOG_DEPTH=1", "DUOLOG_LEAD="+leadID, "DUOLOG_ROLE="+role)
}

// launchArgv wraps a program so that it starts with env and without the Rex
// variables, and needs no shell. Rex starts it directly (--shell=none). Where
// env exists the wrapper is env; Windows has none, so duolog itself sets the
// variables and starts the program (duolog peer launch).
func launchArgv(goos, exe string, env, program []string) []string {
	if goos == "windows" {
		argv := append([]string{exe, "peer", "launch"}, env...)
		return append(append(argv, "--"), program...)
	}
	argv := append([]string{"env", "-u", "REX_SESSION", "-u", "REX_BLOCK"}, env...)
	return append(argv, program...)
}
