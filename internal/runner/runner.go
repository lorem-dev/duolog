// Package runner launches one-off workers with explicit input and logged output.
package runner

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
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/router"
)

const taskInstruction = "Do the task in the stdin block."
const codexAnswerInstruction = "Reply with only one JSON object with the keys done, summary, files and needs. done is true or false, summary is a string, files and needs are arrays of strings."
const defaultTimeout = 30 * time.Minute
const answerSchema = `{"type":"object","properties":{"done":{"type":"boolean"},"summary":{"type":"string"},"files":{"type":"array","items":{"type":"string"}},"needs":{"type":"array","items":{"type":"string"}}},"required":["done","summary","files","needs"],"additionalProperties":false}`

type Spec struct {
	Route                          router.Route
	Mode                           mode.Mode
	Prompt, TaskDir, Cwd, ThreadID string
	Env                            []string
	Allowed, Disallowed            []string
	Stdin                          []byte
	Persistent                     bool
	Timeout                        time.Duration
}

type Usage struct{ Input, Cached, Output int64 }
type Result struct {
	Done        bool
	Summary     string
	Files       []string
	Needs       []string
	Interrupted bool
	ResetAt     time.Time
	SessionID   string
	Usage       Usage
	QuotaEnded  bool
	ExitCode    int
}

// Argv includes the executable. Route.Model is already resolved by the caller;
// version discovery and policy hook installation are outside this package.
func Argv(s Spec) []string {
	switch s.Route.Agent {
	case "codex":
		return codexArgv(s)
	case "claude":
		return claudeArgv(s)
	}
	return nil
}

// Run reads success only from a validated answer.json, even when the CLI exits 0.
func Run(ctx context.Context, s Spec) (result Result, err error) {
	logPath := filepath.Join(s.TaskDir, "stdout.log")
	defer func() {
		if err != nil {
			result.Done = false
			err = fmt.Errorf("worker failed; see %s: %w", logPath, err)
		}
	}()
	if s.TaskDir == "" {
		return result, fmt.Errorf("missing task directory")
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
	argv := Argv(s)
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
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	configureProcess(cmd)
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = s.Cwd
	stdin := s.Stdin
	if s.Route.Agent == "codex" {
		// --output-schema loses the prompt cache, so Codex is asked in plain text.
		stdin = append(append([]byte{}, s.Stdin...), []byte("\n"+codexAnswerInstruction+"\n")...)
	}
	cmd.Stdin = bytes.NewReader(stdin)
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
		defer capture.Close()
		*stream.dest = io.MultiWriter(log, capture)
	}
	cmd.Env = append([]string{}, s.Env...)
	if s.Route.Agent == "claude" {
		env := cmd.Env[:0]
		for _, v := range cmd.Env {
			if !strings.HasPrefix(v, "DISABLE_AUTOUPDATER=") {
				env = append(env, v)
			}
		}
		cmd.Env = append(env, "DISABLE_AUTOUPDATER=1")
	}
	runErr := cmd.Run()
	closeErr := log.Close()
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if ctx.Err() != nil || result.ExitCode < 0 {
		result.Interrupted = true
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, runErr
	}
	if err = readLog(stdoutPath, s.Route.Agent, false, &result); err != nil {
		return result, err
	}
	if err = readLog(stderrPath, s.Route.Agent, true, &result); err != nil {
		return result, err
	}
	if runErr != nil {
		return result, runErr
	}
	if closeErr != nil {
		return result, closeErr
	}
	if s.Route.Agent == "claude" {
		var answer json.RawMessage
		answer, err = claudeResult(stdoutPath, &result)
		if err != nil {
			return result, err
		}
		if err = os.WriteFile(answerPath, answer, 0600); err != nil {
			return result, err
		}
	}
	answer, err := os.ReadFile(answerPath)
	if err != nil {
		return result, err
	}
	if s.Route.Agent == "codex" {
		return codexAnswer(answer, answerPath, result)
	}
	var parsed struct {
		Done    *bool    `json:"done"`
		Summary *string  `json:"summary"`
		Files   []string `json:"files"`
		Needs   []string `json:"needs"`
	}
	decoder := json.NewDecoder(bytes.NewReader(answer))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&parsed); err != nil {
		return result, fmt.Errorf("invalid answer.json: %w", err)
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return result, fmt.Errorf("invalid answer.json: expected one JSON object")
	}
	if parsed.Done == nil || parsed.Summary == nil || parsed.Files == nil || parsed.Needs == nil {
		return result, fmt.Errorf("invalid answer.json: required fields are missing or null")
	}
	result.Done = *parsed.Done
	result.Summary = *parsed.Summary
	result.Files = parsed.Files
	result.Needs = parsed.Needs
	return result, nil
}

func readLog(path, agent string, stderr bool, result *Result) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	for {
		line, readErr := reader.ReadBytes('\n')
		if agent == "codex" && !stderr {
			codexEvent(line, result)
		}
		var event struct {
			Type    string          `json:"type"`
			Message string          `json:"message"`
			Code    string          `json:"code"`
			IsError bool            `json:"is_error"`
			Result  string          `json:"result"`
			Error   json.RawMessage `json:"error"`
		}
		failureEvent := json.Unmarshal(line, &event) == nil && (event.Type == "error" || event.Type == "turn.failed" || agent == "claude" && event.Type == "result" && event.IsError)
		text := ""
		if failureEvent {
			text = event.Message + " " + event.Code + " " + string(event.Error) + " " + event.Result
		} else if stderr && result.ExitCode > 0 {
			text = string(line)
		}
		// Ordinary stdout can contain tool output or model answers about quotas.
		// Only stderr on failure or an explicit error event is authoritative.
		if quotaFailure(text) {
			result.QuotaEnded = true
			if reset := resetTime(text, time.Now()); !reset.IsZero() {
				result.ResetAt = reset
			} else if reset := codexResetTime(text, time.Now()); !reset.IsZero() {
				result.ResetAt = reset
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
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

var resetPattern = regexp.MustCompile(`resets ([0-9]{1,2})(am|pm) \(([^)]+)\)`)

func resetTime(text string, now time.Time) time.Time {
	match := resetPattern.FindStringSubmatch(text)
	if match == nil {
		return time.Time{}
	}
	hour, _ := strconv.Atoi(match[1])
	if hour < 1 || hour > 12 {
		return time.Time{}
	}
	loc, err := time.LoadLocation(match[3])
	if err != nil {
		return time.Time{}
	}
	hour %= 12
	if match[2] == "pm" {
		hour += 12
	}
	local := now.In(loc)
	reset := time.Date(local.Year(), local.Month(), local.Day(), hour, 0, 0, 0, loc)
	if !reset.After(now) {
		reset = reset.AddDate(0, 0, 1)
	}
	return reset
}

var codexResetPattern = regexp.MustCompile(`(?i)try again at ([0-9]{1,2}):([0-9]{2}) ?(am|pm)`)

// codexResetTime parses "try again at 8:48 AM" as the next such local time.
func codexResetTime(text string, now time.Time) time.Time {
	match := codexResetPattern.FindStringSubmatch(text)
	if match == nil {
		return time.Time{}
	}
	hour, _ := strconv.Atoi(match[1])
	minute, _ := strconv.Atoi(match[2])
	if hour < 1 || hour > 12 || minute > 59 {
		return time.Time{}
	}
	hour %= 12
	if strings.EqualFold(match[3], "pm") {
		hour += 12
	}
	reset := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !reset.After(now) {
		reset = reset.AddDate(0, 0, 1)
	}
	return reset
}

// ResetTime parses the time a provider limit message says the quota returns,
// for either agent. It returns the zero time when the text names none.
func ResetTime(text string, now time.Time) time.Time {
	if reset := resetTime(text, now); !reset.IsZero() {
		return reset
	}
	return codexResetTime(text, now)
}
