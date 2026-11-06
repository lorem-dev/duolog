package wiring

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/core/run"
)

// The worker tests that were runner's run both agents through their Workers.
// This file gives them the names runner had: Argv, Run and Launch dispatch on
// the spec's agent, as runner did, and the launch types are core/run's.
type (
	Spec   = run.Spec
	MCP    = run.MCP
	Usage  = run.Usage
	Result = run.Result
)

const (
	CodeNone  = run.CodeNone
	CodeQuota = run.CodeQuota
	CodeAuth  = run.CodeAuth
	CodeExit  = run.CodeExit
	CodeParse = run.CodeParse

	taskInstruction = kit.TaskInstruction
)

var ErrTurnCap = run.ErrTurnCap

// codexAnswerInstruction is the sentence Codex's stdin ends with.
var codexAnswerInstruction = strings.TrimSpace(string(codex.Worker{}.Stdin(Spec{})))

// dialect is the worker of the spec's agent, which is also its argv builder.
type dialect interface {
	Run(ctx context.Context, s Spec) (Result, error)
	Launch(s Spec) []string
	Argv(s Spec) []string
	Scan(stdout []byte, s Spec, r *Result, interrupted bool) ([]byte, error)
}

func dialectOf(agent string) dialect {
	if agent == "codex" {
		return codex.Worker{}
	}
	return claude.Worker{}
}

func Argv(s Spec) []string                            { return dialectOf(s.Route.Agent).Argv(s) }
func Launch(s Spec) []string                          { return dialectOf(s.Route.Agent).Launch(s) }
func Run(ctx context.Context, s Spec) (Result, error) { return dialectOf(s.Route.Agent).Run(ctx, s) }

// ResetTime parses a Claude or Codex limit message, by the phrase it holds.
func ResetTime(text string, now time.Time) time.Time {
	if strings.Contains(strings.ToLower(text), "try again at") {
		return codex.ResetTime(text, now)
	}
	return claude.ResetTime(text, now)
}

// readClaudeStream folds a Claude stdout capture the way a run does.
func readClaudeStream(path string, r *Result) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return claude.Worker{}.Scan(data, Spec{}, r, false)
}

// codexEvent folds one Codex event line the way a run does.
func codexEvent(line []byte, r *Result) { _, _ = codex.Worker{}.Scan(line, Spec{}, r, false) }

// codexAnswer reads Codex's final message the way a run does.
func codexAnswer(message []byte, answerPath string, r Result) (Result, error) {
	return codex.Worker{}.Fold(nil, message, nil, Spec{TaskDir: filepath.Dir(answerPath)}, r)
}

// checkProcess returns the refusal a run makes before it starts anything: no
// process, another agent's, words other than the admitted ones, or bridged env
// the build never saw. It returns nil when none applies. The spec has no
// model, so a run that passes the check stops at "missing model".
func checkProcess(s Spec) error {
	dir, mkErr := os.MkdirTemp("", "checkprocess-")
	if mkErr != nil {
		return mkErr
	}
	defer os.RemoveAll(dir)
	s.TaskDir, s.Route.Model = dir, ""
	// Every refusal comes before the model check.
	if _, err := Run(context.Background(), s); err != nil && !strings.Contains(err.Error(), "missing model") {
		return err
	}
	return nil
}
