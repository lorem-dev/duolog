// Package process runs one worker for an agent's Dialect: the agent-free part
// of a worker run. It is a package of its own so that internal/adapter/kit,
// which the config adapter imports, does not depend on the run types.
package process

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/core/run"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

const defaultTimeout = 30 * time.Minute

// Dialect is what Run asks of an agent: the parts of a worker run that differ
// between agents. Each agent's Worker implements it. Run does the rest: the
// task folder and log, the timeout, the program on the process's PATH, the
// process tree and the failure classification.
type Dialect interface {
	// Launch is what the worker's process carries after its prefix, in
	// workerenv.ProcessInput.Launch: the operation, then the permission words.
	Launch(s run.Spec) []string
	// Argv includes the executable: the Process prefix, then the run's flags.
	Argv(s run.Spec) []string
	// Stdin is what the worker reads on its standard input.
	Stdin(s run.Spec) []byte
	// Prepare makes what the argv names before the start: Claude writes its
	// MCP config into s.MCP.ConfigPath. cleanup may be nil.
	Prepare(dir string, s *run.Spec) (cleanup func(), err error)
	// Scan reads what stdout says about the run into r, after every exit:
	// Claude's stream (session, usage, cost, billing) always, Codex's events
	// only when the run was not interrupted. It returns the structured answer
	// that Run saves as answer.json, or nil when the agent writes that file
	// itself.
	Scan(stdout []byte, s run.Spec, r *run.Result, interrupted bool) (answer []byte, err error)
	// ErrorEvent reports whether an event of this type, with this is_error, is
	// an error event of this agent beyond the types that every agent shares.
	ErrorEvent(eventType string, isError bool) bool
	// AuthServer names the MCP server in an error text, or returns "".
	AuthServer(text string) string
	// Reset parses the time a limit message says the quota returns, or returns
	// the zero time.
	Reset(text string, now time.Time) time.Time
	// Fold turns the final answer into the result. answer is the content of
	// answer.json; answerErr is the error of reading it, which only a skill
	// test passes on (a normal run fails before Fold).
	Fold(stdout, answer []byte, answerErr error, s run.Spec, r run.Result) (run.Result, error)
}

// missingBinary is the error of a worker whose program is not on its PATH. Its
// text is the cause's; it also matches run.ErrBinaryMissing.
type missingBinary struct{ err error }

func (e missingBinary) Error() string   { return e.err.Error() }
func (e missingBinary) Unwrap() []error { return []error{e.err, run.ErrBinaryMissing} }

// Run reads success only from a validated answer.json, even when the CLI exits 0.
func Run(ctx context.Context, s run.Spec, d Dialect) (result run.Result, err error) {
	logPath := filepath.Join(s.TaskDir, "stdout.log")
	defer func() {
		if err != nil {
			result.Done = false
			result.SetCode(run.CodeExit)
			err = fmt.Errorf("worker failed; see %s: %w", logPath, err)
		}
		result.SetCode(run.CodeNone)
	}()
	if s.TaskDir == "" {
		return result, fmt.Errorf("missing task directory")
	}
	if err = checkProcess(s, d.Launch(s)); err != nil {
		return result, err
	}
	s.TaskDir, err = filepath.Abs(s.TaskDir)
	if err != nil {
		return result, err
	}
	logPath = filepath.Join(s.TaskDir, "stdout.log")
	if err = os.MkdirAll(s.TaskDir, 0755); err != nil {
		return result, err
	}
	answerPath := filepath.Join(s.TaskDir, "answer.json")
	// A previous run must never supply this invocation's answer.
	if err = os.Remove(answerPath); err != nil && !os.IsNotExist(err) {
		return result, err
	}
	cleanup, err := d.Prepare(s.TaskDir, &s)
	if err != nil {
		return result, err
	}
	if cleanup != nil {
		defer cleanup()
	}
	argv := d.Argv(s)
	if len(argv) == 0 {
		return result, fmt.Errorf("unknown agent %q", s.Route.Agent)
	}
	if s.Route.Model == "" {
		return result, fmt.Errorf("missing model")
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return result, err
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// The program is the one on the process's own PATH.
	bin, lookErr := workerenv.LookPath(argv[0], s.Process.Env)
	if lookErr != nil {
		log.Close()
		if errors.Is(lookErr, exec.ErrNotFound) {
			lookErr = missingBinary{lookErr}
		}
		return result, lookErr
	}
	cmd := exec.CommandContext(ctx, bin, argv[1:]...)
	cmd.Args[0] = argv[0]
	kit.ProcessTree(cmd)
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = s.Cwd
	cmd.Stdin = bytes.NewReader(d.Stdin(s))
	// Keep separate disk captures for parsing results and failure signals.
	// Both streams are also retained in the combined diagnostic log.
	stdoutPath := filepath.Join(s.TaskDir, s.Route.Agent+".stdout.json")
	stderrPath := filepath.Join(s.TaskDir, "stderr.log")
	for _, stream := range []struct {
		path string
		dest *io.Writer
	}{{stdoutPath, &cmd.Stdout}, {stderrPath, &cmd.Stderr}} {
		capture, openErr := os.OpenFile(stream.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if openErr != nil {
			log.Close()
			return result, openErr
		}
		defer capture.Close() //nolint:gocritic // two streams, both held until the worker has run
		*stream.dest = io.MultiWriter(log, capture)
	}
	// The built environment, bridged MCP env included, and nothing else; Build
	// sets Claude's updater and auto memory switches itself.
	cmd.Env = slices.Clone(s.Process.Env)
	runErr := cmd.Start()
	if runErr == nil && s.Started != nil {
		if attachErr := s.Started(cmd.Process.Pid); attachErr != nil {
			// A child that cannot be recorded does not run on unseen.
			cancel()
			_ = cmd.Wait()
			log.Close()
			return result, fmt.Errorf("record the worker process: %w", attachErr)
		}
	}
	if runErr == nil {
		runErr = cmd.Wait()
	}
	closeErr := log.Close()
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	stdout, stdoutErr := os.ReadFile(stdoutPath)
	interrupted := ctx.Err() != nil || result.ExitCode < 0
	// The agent's stream holds the usage, cost and billing evidence of the run
	// so far, so it is read on every exit, a cancelled one too.
	answer, scanErr := d.Scan(stdout, s, &result, interrupted)
	if interrupted {
		result.Interrupted = true
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			result.SetCode(run.CodeTimeout)
		case ctx.Err() != nil:
			// The caller cancelled the run; it did not fail by itself.
			result.SetCode(run.CodeNone)
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, runErr
	}
	if stdoutErr != nil {
		return result, stdoutErr
	}
	var fallback time.Time
	scanFailures(stdout, false, d, &result, &fallback)
	stderr, err := os.ReadFile(stderrPath)
	if err != nil {
		return result, err
	}
	scanFailures(stderr, true, d, &result, &fallback)
	if result.QuotaEnded && result.ResetAt.IsZero() {
		result.ResetAt = fallback
	}
	if result.QuotaEnded {
		result.SetCode(run.CodeQuota)
	} else if result.AuthFailed {
		result.SetCode(run.CodeAuth)
	}
	if s.SkillTest {
		data, readErr := os.ReadFile(answerPath)
		result, err = d.Fold(stdout, data, readErr, s, result)
		if errors.Is(runErr, exec.ErrWaitDelay) && result.ExitCode == 0 {
			runErr = nil
		}
		return result, errors.Join(runErr, closeErr, err)
	}
	// A worker at its turn cap has no answer, but its usage counts.
	if result.TurnsEnded {
		return result, run.ErrTurnCap
	}
	// A worker that exits 0 and leaves a background child holding its output
	// has still finished: its answer is complete.
	if runErr != nil && (!errors.Is(runErr, exec.ErrWaitDelay) || result.ExitCode != 0) {
		return result, runErr
	}
	if closeErr != nil {
		return result, closeErr
	}
	if scanErr != nil {
		result.SetCode(run.CodeParse)
		return result, scanErr
	}
	if answer != nil {
		if err = os.WriteFile(answerPath, answer, 0600); err != nil {
			return result, err
		}
	}
	data, err := os.ReadFile(answerPath)
	if err != nil {
		// The worker exited without leaving its answer.
		result.SetCode(run.CodeParse)
		return result, err
	}
	return d.Fold(stdout, data, nil, s, result)
}

// checkProcess refuses a launch whose environment would not be the built
// worker process of its route: no process, another agent's or shape's, or
// bridged MCP env that the build never saw, which would bypass its route
// detection. The refusal names no value.
func checkProcess(s run.Spec, launch []string) error {
	p := s.Process
	switch {
	case p == nil:
		return errors.New("no built worker process; build one with workerenv.Build")
	case p.Agent != s.Route.Agent || p.Shape != workerenv.Worker:
		return fmt.Errorf("the process is a %s %s, not a %s worker", p.Agent, p.Shape, s.Route.Agent)
	case !slices.Equal(p.Launch, launch):
		// The worker adds no permission word after admission: what starts
		// is what the gate admitted, or nothing does.
		return fmt.Errorf("the admitted process launches with %q, not this run's %q; build it with the agent's Worker.Launch",
			strings.Join(p.Launch, " "), strings.Join(launch, " "))
	}
	if s.MCP != nil {
		for _, pair := range s.MCP.Env {
			// A route name the build withheld stays withheld.
			if name, _, _ := strings.Cut(pair, "="); !slices.Contains(p.Env, pair) && !slices.Contains(p.Dropped, name) {
				return fmt.Errorf("bridged MCP variable %s is not in the built worker process", name)
			}
		}
	}
	return nil
}

// scanFailures keeps in fallback the furthest kit.FallbackReset of a limit
// line that names no reset, for Run to use when no line names one.
func scanFailures(data []byte, stderr bool, d Dialect, result *run.Result, fallback *time.Time) {
	reader := bufio.NewReader(bytes.NewReader(data))
	for {
		line, readErr := reader.ReadBytes('\n')
		var event struct {
			Type    string          `json:"type"`
			Message string          `json:"message"`
			Code    string          `json:"code"`
			IsError bool            `json:"is_error"`
			Result  string          `json:"result"`
			Error   json.RawMessage `json:"error"`
		}
		failureEvent := json.Unmarshal(line, &event) == nil && (event.Type == "error" || event.Type == "turn.failed" || d.ErrorEvent(event.Type, event.IsError))
		text := ""
		if failureEvent {
			text = event.Message + " " + event.Code + " " + string(event.Error) + " " + event.Result
		} else if stderr && result.ExitCode > 0 {
			text = string(line)
		}
		// Ordinary stdout can contain tool output or model answers about quotas.
		// Only stderr on failure or an explicit error event is authoritative.
		if !result.AuthFailed && authFailure(text) {
			result.AuthFailed = true
			result.AuthServer = d.AuthServer(text)
		}
		if quotaFailure(text) {
			result.QuotaEnded = true
			if reset := d.Reset(text, time.Now()); !reset.IsZero() {
				result.ResetAt = reset
			} else if reset := kit.FallbackReset(text, time.Now()); reset.After(*fallback) {
				*fallback = reset
			}
		}
		if readErr != nil {
			return
		}
	}
}

// authFailure finds a refused login in an error. Like quotaFailure it reads
// only authoritative text: stderr of a failed run and error events.
func authFailure(text string) bool {
	text = strings.ToLower(text)
	for _, phrase := range []string{"not logged in", "please run /login", "invalid api key", "oauth token has expired", "authentication_error", "401 unauthorized", "codex login", "token_expired", "invalid_token", "refresh token"} {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func quotaFailure(text string) bool {
	text = strings.ToLower(text)
	for _, phrase := range []string{"hit your", "usage limit", "rate_limit", "http 429", "quota exceeded", "insufficient_quota"} {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}
