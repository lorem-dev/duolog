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
	"slices"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/router"
	"github.com/lorem-dev/duolog/internal/usage"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

const taskInstruction = "Do the task in the stdin block."
const codexAnswerInstruction = "Reply with only one JSON object with the keys done, summary, files, needs and decisions. done is true or false, summary is a string, files, needs and decisions are arrays of strings."
const defaultTimeout = 30 * time.Minute

// ErrTurnCap is Run's error for a worker that reached Spec.MaxTurns.
var ErrTurnCap = errors.New("worker reached its turn cap")

const answerSchema = `{"type":"object","properties":{"done":{"type":"boolean"},"summary":{"type":"string"},"files":{"type":"array","items":{"type":"string"}},"needs":{"type":"array","items":{"type":"string"}},"decisions":{"type":"array","items":{"type":"string"}}},"required":["done","summary","files","needs","decisions"],"additionalProperties":false}`

// Spec is one worker launch. Process is the built worker process of the
// route's agent: its environment, its argv prefix and its settings, the same
// process its login check and quota read used. There is no other environment
// and no fallback to duolog's: a launch without a Process is refused, and
// bridged MCP env must already be in it. Started, when set, receives the
// child's process ID once it started and before Run waits for it; an error
// stops the child.
type Spec struct {
	Route                          router.Route
	Mode                           mode.Mode
	Prompt, TaskDir, Cwd, ThreadID string
	Process                        *workerenv.Process
	Started                        func(pid int) error
	Stdin                          []byte
	Persistent                     bool
	// KeepMCP keeps the agent's MCP servers for a task that needs one.
	KeepMCP bool
	// KeepSkills keeps the skill list of a minimal worker whose task needs a skill.
	KeepSkills bool
	Timeout    time.Duration
	// MaxTurns caps a Claude worker's turns with --max-turns; 0 sets none.
	// Codex has no such flag and ignores it.
	MaxTurns int
	// SkillTest asks for raw situation answers without tools or a worker schema.
	SkillTest bool
	// MCP holds servers bridged from the lead's agent for this run, or nil.
	MCP *MCP
	// ProtectedRoots are the telemetry and balance roots. The caller builds
	// them into Process, whose Claude settings deny them to an enabled sandbox.
	ProtectedRoots []string
	// Native is the resolved native permission name; empty means mode.Args.
	// It is used only when it is known, not above Mode, and fits the sandbox.
	Native string
	// Network and WritableRoots are Codex extras for a workspace-write
	// worker at level edits or full. Claude ignores them.
	Network       bool
	WritableRoots []string
}

// Launch is what a worker's process carries after its prefix, in
// workerenv.ProcessInput.Launch: the operation, then the permission words.
// For Codex, exec and its sandbox flag with both pins, as the run puts them
// before resume; for Claude, -p and its permission flags. A skill test's
// Codex run is exec with a read-only sandbox, its Claude run -p alone. The
// caller builds them into the process the gate admits; Run refuses a process
// whose launch words differ.
func Launch(s Spec) []string {
	operation := map[string]string{"codex": "exec", "claude": "-p"}[s.Route.Agent]
	switch {
	case operation == "":
		return nil
	case s.SkillTest && s.Route.Agent == "codex":
		return []string{operation, "--sandbox", "read-only"}
	case s.SkillTest:
		return []string{operation}
	}
	return append([]string{operation}, permissionArgs(s.Route.Agent, s)...)
}

// permissionArgs returns the native permission flags for a worker. It falls
// back to mode.Args when Native is empty, unknown, above Mode, or not the
// read-only sandbox's own default (Claude default, Codex read-only) under a
// read-only sandbox. A Claude worker never gets plan: mode.Flags gives it
// default, and the gate keeps level plan read-only. A Codex workspace-write launch
// always pins network_access and writable_roots, so that Codex's own config
// files cannot widen the worker; the extras apply only on a resolved
// workspace-write at edits or full.
func permissionArgs(agent string, s Spec) []string {
	args, extras := mode.Args(agent, s.Mode, s.Route.Sandbox), false
	_, known := mode.Rank(agent, s.Native)
	switch {
	case s.Native == "" || !known || mode.Lower(mode.Parse(agent, s.Native), s.Mode) != mode.Parse(agent, s.Native):
	case s.Route.Sandbox == "read-only" && s.Native != mode.Default(agent, mode.Plan, "read-only"):
	default:
		args = mode.Flags(agent, s.Native)
		extras = agent == "codex" && s.Native == "workspace-write" && (s.Mode == mode.Edits || s.Mode == mode.Full)
	}
	if !extras {
		return mode.PinCodexSandbox(agent, args, false, nil)
	}
	return mode.PinCodexSandbox(agent, args, s.Network, s.WritableRoots)
}

// MCP is what a worker needs to start servers bridged from the lead's agent.
// The config parts refer to values by variable name only; the values are in
// Env, which the caller builds into the worker's Process and nothing else.
type MCP struct {
	// Names are the bridged servers.
	Names []string
	// CodexArgs are -c overrides for a Codex worker.
	CodexArgs []string
	// ClaudeConfig is the --mcp-config JSON for a Claude worker. Run writes it to
	// a 0600 file in the task folder and removes the file after the run.
	ClaudeConfig []byte
	// AllowedTools and DisallowedTools are Claude tool names, such as
	// mcp__tracker__get_issue.
	AllowedTools, DisallowedTools []string
	// Env holds NAME=value pairs for the worker process.
	Env []string
	// configPath is where Run wrote ClaudeConfig.
	configPath string
}

// Usage is total input with its cache reads and writes, and output.
type Usage = usage.Tokens

// Error codes say why a run failed. Run sets one where the run failed: the
// process, the timeout, the logs it classified, the answer it parsed; never
// from an error's text. The gate code is the caller's, which sees the gate's
// decisions.
const (
	CodeNone    = "none"
	CodeQuota   = "quota"
	CodeAuth    = "auth"
	CodeTimeout = "timeout"
	CodeExit    = "exit"
	CodeParse   = "parse"
	CodeGate    = "gate"
)

type Result struct {
	RawAnswer string
	Done      bool
	Summary   string
	Files     []string
	Needs     []string
	// Decisions are the choices the task left open that the worker made itself.
	Decisions   []string
	Interrupted bool
	ResetAt     time.Time
	SessionID   string
	Usage       Usage
	QuotaEnded  bool
	// TurnsEnded means the worker reached Spec.MaxTurns and gave no answer.
	TurnsEnded bool
	ExitCode   int
	// AuthFailed means an error said the worker's agent or a server refused
	// its credentials. AuthServer names the server, when the error did.
	AuthFailed bool
	AuthServer string
	// Billing is the run's own billing evidence, from the rate limit events of
	// Claude's stream. Codex sends none, so it stays empty.
	Billing quota.RunEvidence
	// ReportedUSD is the cost the CLI reported for the run, or nil when it
	// reported none or a value that is not a finite cost of zero or more.
	ReportedUSD *float64
	// UsageKnown means the vendor reported the usage; zero usage is then a
	// count, not a gap.
	UsageKnown bool
	// UsageComplete means the usage is the whole run's: Claude's result event
	// carried it, or Codex's turn completed. A run cancelled or timed out
	// mid-request has known usage of its finished messages only, and the
	// request in flight is billed too.
	UsageComplete bool
	// ErrorCode is one of the Code constants; CodeNone after a run that did
	// not fail.
	ErrorCode string
}

// setCode keeps the first code set: the place the run failed first.
func setCode(r *Result, code string) {
	if r.ErrorCode == "" {
		r.ErrorCode = code
	}
}

// Argv includes the executable: the Process prefix once, with its global
// overrides and settings, then the run's own flags. Route.Model is already
// resolved by the caller; version discovery and policy hook installation are
// outside this package. Without a Process there is no argv.
func Argv(s Spec) []string {
	if s.Process == nil {
		return nil
	}
	if s.SkillTest {
		return skillArgv(s)
	}
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
			setCode(&result, CodeExit)
			err = fmt.Errorf("worker failed; see %s: %w", logPath, err)
		}
		setCode(&result, CodeNone)
	}()
	if s.TaskDir == "" {
		return result, fmt.Errorf("missing task directory")
	}
	if err = checkProcess(s); err != nil {
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
	if s.MCP != nil && s.Route.Agent == "claude" && len(s.MCP.ClaudeConfig) > 0 {
		bridged := *s.MCP
		if bridged.configPath, err = writeMCPConfig(s.TaskDir, bridged.ClaudeConfig); err != nil {
			return result, err
		}
		defer os.Remove(bridged.configPath)
		s.MCP = &bridged
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
	// The program is the one on the process's own PATH.
	bin, lookErr := workerenv.LookPath(argv[0], s.Process.Env)
	if lookErr != nil {
		log.Close()
		return result, lookErr
	}
	cmd := exec.CommandContext(ctx, bin, argv[1:]...)
	cmd.Args[0] = argv[0]
	configureProcess(cmd)
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = s.Cwd
	stdin := s.Stdin
	if s.Route.Agent == "codex" && !s.SkillTest {
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
	// Claude's stream holds the usage, cost and billing evidence of the run so
	// far, so it is read on every exit, a cancelled one too.
	var claudeAnswer json.RawMessage
	var claudeErr error
	if s.Route.Agent == "claude" && !s.SkillTest {
		claudeAnswer, claudeErr = readClaudeStream(stdoutPath, &result)
	}
	if ctx.Err() != nil || result.ExitCode < 0 {
		result.Interrupted = true
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			setCode(&result, CodeTimeout)
		case ctx.Err() != nil:
			// The caller cancelled the run; it did not fail by itself.
			setCode(&result, CodeNone)
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, runErr
	}
	var fallback time.Time
	if err = readLog(stdoutPath, s.Route.Agent, false, &result, &fallback); err != nil {
		return result, err
	}
	if err = readLog(stderrPath, s.Route.Agent, true, &result, &fallback); err != nil {
		return result, err
	}
	if result.QuotaEnded && result.ResetAt.IsZero() {
		result.ResetAt = fallback
	}
	if result.QuotaEnded {
		setCode(&result, CodeQuota)
	} else if result.AuthFailed {
		setCode(&result, CodeAuth)
	}
	if s.SkillTest {
		result, err = skillResult(s, stdoutPath, answerPath, result)
		if errors.Is(runErr, exec.ErrWaitDelay) && result.ExitCode == 0 {
			runErr = nil
		}
		return result, errors.Join(runErr, closeErr, err)
	}
	// A worker at its turn cap has no answer, but its usage counts.
	if result.TurnsEnded {
		return result, ErrTurnCap
	}
	// A worker that exits 0 and leaves a background child holding its output
	// has still finished: its answer is complete.
	if runErr != nil && (!errors.Is(runErr, exec.ErrWaitDelay) || result.ExitCode != 0) {
		return result, runErr
	}
	if closeErr != nil {
		return result, closeErr
	}
	if s.Route.Agent == "claude" {
		if claudeErr != nil {
			setCode(&result, CodeParse)
			return result, claudeErr
		}
		if err = os.WriteFile(answerPath, claudeAnswer, 0600); err != nil {
			return result, err
		}
	}
	answer, err := os.ReadFile(answerPath)
	if err != nil {
		// The worker exited without leaving its answer.
		setCode(&result, CodeParse)
		return result, err
	}
	if s.Route.Agent == "codex" {
		return codexAnswer(answer, answerPath, result)
	}
	var parsed struct {
		Done      *bool    `json:"done"`
		Summary   *string  `json:"summary"`
		Files     []string `json:"files"`
		Needs     []string `json:"needs"`
		Decisions []string `json:"decisions"`
	}
	decoder := json.NewDecoder(bytes.NewReader(answer))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&parsed); err != nil {
		setCode(&result, CodeParse)
		return result, fmt.Errorf("invalid answer.json: %w", err)
	}
	var extra any
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		setCode(&result, CodeParse)
		return result, fmt.Errorf("invalid answer.json: expected one JSON object")
	}
	if parsed.Done == nil || parsed.Summary == nil || parsed.Files == nil || parsed.Needs == nil {
		setCode(&result, CodeParse)
		return result, fmt.Errorf("invalid answer.json: required fields are missing or null")
	}
	result.Done = *parsed.Done
	result.Summary = *parsed.Summary
	result.Files = parsed.Files
	result.Needs = parsed.Needs
	result.Decisions = parsed.Decisions
	return result, nil
}

// checkProcess refuses a launch whose environment would not be the built
// worker process of its route: no process, another agent's or shape's, or
// bridged MCP env that the build never saw, which would bypass its route
// detection. The refusal names no value.
func checkProcess(s Spec) error {
	p := s.Process
	switch {
	case p == nil:
		return errors.New("no built worker process; build one with workerenv.Build")
	case p.Agent != s.Route.Agent || p.Shape != workerenv.Worker:
		return fmt.Errorf("the process is a %s %s, not a %s worker", p.Agent, p.Shape, s.Route.Agent)
	case !slices.Equal(p.Launch, Launch(s)):
		// The runner adds no permission word after admission: what starts
		// is what the gate admitted, or nothing does.
		return fmt.Errorf("the admitted process launches with %q, not this run's %q; build it with runner.Launch",
			strings.Join(p.Launch, " "), strings.Join(Launch(s), " "))
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

// writeMCPConfig writes the bridged servers for --mcp-config. CreateTemp makes
// the file 0600; the chmod keeps it so on a system that does otherwise.
func writeMCPConfig(dir string, data []byte) (string, error) {
	f, err := os.CreateTemp(dir, "mcp-*.json")
	if err != nil {
		return "", err
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if err = errors.Join(writeErr, closeErr, os.Chmod(f.Name(), 0600)); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// readLog keeps in fallback the furthest fallbackReset of a limit line that names
// no reset, for Run to use when no line names one.
func readLog(path, agent string, stderr bool, result *Result, fallback *time.Time) error {
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
		if !result.AuthFailed && authFailure(text) {
			result.AuthFailed = true
			if m := mcpServerPattern.FindStringSubmatch(text); m != nil {
				result.AuthServer = m[1]
			}
		}
		if quotaFailure(text) {
			result.QuotaEnded = true
			if reset := namedReset(text, time.Now()); !reset.IsZero() {
				result.ResetAt = reset
			} else if reset := fallbackReset(text, time.Now()); reset.After(*fallback) {
				*fallback = reset
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

// mcpServerPattern names the server in Codex's MCP start-up errors.
var mcpServerPattern = regexp.MustCompile("MCP client for [`'\"]?([A-Za-z0-9_.-]+)[`'\"]? failed")

func quotaFailure(text string) bool {
	text = strings.ToLower(text)
	for _, phrase := range []string{"hit your", "usage limit", "rate_limit", "http 429", "quota exceeded", "insufficient_quota"} {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

// The zone may follow on the next line of a pane; a parenthesis that is not a
// zone name, such as "(error type rate_limit, HTTP 429)", is not taken for one.
var resetPattern = regexp.MustCompile(`resets (?:([A-Za-z]{3,9}) ([0-9]{1,2})(?:,| at)? )?([0-9]{1,2})(?::([0-9]{2}))? ?(am|pm)(?:\s+\(([A-Za-z_]+(?:/[A-Za-z0-9_+-]+)*)\))?`)

// resetTime parses Claude's "resets 8:40pm (Europe/London)" as the next such
// time in that zone, or in the zone of now when the text names none. A weekly
// reset that names a day, as "resets Nov 10, 5pm", is the next such day.
func resetTime(text string, now time.Time) time.Time {
	match := resetPattern.FindStringSubmatch(text)
	if match == nil {
		return time.Time{}
	}
	hour, _ := strconv.Atoi(match[3])
	minute := 0
	if match[4] != "" {
		minute, _ = strconv.Atoi(match[4])
	}
	if hour < 1 || hour > 12 || minute > 59 {
		return time.Time{}
	}
	loc := now.Location()
	if match[6] != "" {
		var err error
		if loc, err = time.LoadLocation(match[6]); err != nil {
			return time.Time{}
		}
	}
	hour %= 12
	if match[5] == "pm" {
		hour += 12
	}
	local := now.In(loc)
	if match[1] == "" {
		reset := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
		if !reset.After(now) {
			reset = reset.AddDate(0, 0, 1)
		}
		return reset
	}
	month, err := time.Parse("Jan", match[1])
	if err != nil {
		if month, err = time.Parse("January", match[1]); err != nil {
			return time.Time{}
		}
	}
	day, _ := strconv.Atoi(match[2])
	for year := local.Year(); year <= local.Year()+1; year++ {
		reset := time.Date(year, month.Month(), day, hour, minute, 0, 0, loc)
		if reset.Month() != month.Month() || reset.Day() != day {
			return time.Time{}
		}
		if reset.After(now) {
			return reset
		}
	}
	return time.Time{}
}

var fiveHourPattern = regexp.MustCompile(`(?i)\b(?:5|five)[- ]hour`)

// fallbackReset is how long a limit without a readable reset is kept: the
// whole window for a five-hour limit, an hour otherwise, so that it ends by
// itself rather than staying until a probe.
func fallbackReset(text string, now time.Time) time.Time {
	if fiveHourPattern.MatchString(text) {
		return now.Add(5 * time.Hour)
	}
	return now.Add(time.Hour)
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
// for either agent. When the text names none it returns fallbackReset, never
// the zero time.
func ResetTime(text string, now time.Time) time.Time {
	if reset := namedReset(text, now); !reset.IsZero() {
		return reset
	}
	return fallbackReset(text, now)
}

func namedReset(text string, now time.Time) time.Time {
	if reset := resetTime(text, now); !reset.IsZero() {
		return reset
	}
	return codexResetTime(text, now)
}

// ProcessTree makes cancelling cmd end the processes it started too, as for a
// worker.
func ProcessTree(cmd *exec.Cmd) { configureProcess(cmd) }
