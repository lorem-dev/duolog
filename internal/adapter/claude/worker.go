package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"time"
	_ "time/tzdata"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/adapter/kit/process"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/run"
)

const answerSchema = `{"type":"object","properties":{"done":{"type":"boolean"},"summary":{"type":"string"},"files":{"type":"array","items":{"type":"string"}},"needs":{"type":"array","items":{"type":"string"}},"decisions":{"type":"array","items":{"type":"string"}}},"required":["done","summary","files","needs","decisions"],"additionalProperties":false}`

// Worker runs Claude workers. It is the process.Dialect of Claude.
type Worker struct{}

// Run starts one Claude worker and reads its answer.
func (w Worker) Run(ctx context.Context, s run.Spec) (run.Result, error) {
	return process.Run(ctx, s, w)
}

// Launch is what a worker's process carries after its prefix, in
// workerenv.ProcessInput.Launch: -p, then the permission flags. A skill test's
// run is -p alone. The caller builds them into the process the gate admits;
// Run refuses a process whose launch words differ.
func (Worker) Launch(s run.Spec) []string {
	if s.SkillTest {
		return []string{"-p"}
	}
	return append([]string{"-p"}, permissionArgs(s)...)
}

// permissionArgs returns the native permission flags for a worker. It falls
// back to mode.Args when Native is empty, unknown, above Mode, or not the
// read-only sandbox's own default (default under a read-only sandbox). A
// Claude worker never gets plan: mode.Flags gives it default, and the gate
// keeps level plan read-only.
func permissionArgs(s run.Spec) []string {
	args := mode.Args("claude", s.Mode, s.Route.Sandbox)
	_, known := mode.Rank("claude", s.Native)
	switch {
	case s.Native == "" || !known || mode.Lower(mode.Parse("claude", s.Native), s.Mode) != mode.Parse("claude", s.Native):
	case s.Route.Sandbox == "read-only" && s.Native != mode.Default("claude", mode.Plan, "read-only"):
	default:
		args = mode.Flags("claude", s.Native)
	}
	return mode.PinCodexSandbox("claude", args, false, nil)
}

// Argv includes the executable: the Process prefix once, with its global
// overrides and settings, then the run's own flags. Route.Model is already
// resolved by the caller; version discovery and policy hook installation are
// outside this package. Without a Process there is no argv.
func (Worker) Argv(s run.Spec) []string {
	if s.Process == nil {
		return nil
	}
	if s.SkillTest {
		return skillArgv(s)
	}
	return claudeArgv(s)
}

// Stdin is the task block as it is.
func (Worker) Stdin(s run.Spec) []byte { return s.Stdin }

// Prepare writes the bridged servers into the task folder for --mcp-config and
// removes the file after the run.
func (Worker) Prepare(dir string, s *run.Spec) (func(), error) {
	if s.MCP == nil || len(s.MCP.ClaudeConfig) == 0 {
		return nil, nil
	}
	bridged := *s.MCP
	path, err := kit.WriteMCPConfig(dir, bridged.ClaudeConfig)
	if err != nil {
		return nil, err
	}
	bridged.ConfigPath = path
	s.MCP = &bridged
	return func() { os.Remove(path) }, nil
}

// Scan folds Claude's stream, whatever the run's exit, so a failed or
// cancelled run keeps what it measured.
func (Worker) Scan(stdout []byte, s run.Spec, r *run.Result, _ bool) ([]byte, error) {
	if s.SkillTest {
		return nil, nil
	}
	return readClaudeStream(stdout, r)
}

// ErrorEvent: a result event that says it is an error.
func (Worker) ErrorEvent(eventType string, isError bool) bool {
	return eventType == "result" && isError
}

// AuthServer names no server: Claude's login errors do not.
func (Worker) AuthServer(string) string { return "" }

// Reset parses Claude's "resets 8:40pm (Europe/London)".
func (Worker) Reset(text string, now time.Time) time.Time { return resetTime(text, now) }

// Fold reads the answer Run saved from the stream.
func (Worker) Fold(stdout, answer []byte, _ error, s run.Spec, result run.Result) (run.Result, error) {
	if s.SkillTest {
		return skillResult(stdout, result)
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
	if err := decoder.Decode(&parsed); err != nil {
		result.SetCode(run.CodeParse)
		return result, fmt.Errorf("invalid answer.json: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		result.SetCode(run.CodeParse)
		return result, fmt.Errorf("invalid answer.json: expected one JSON object")
	}
	if parsed.Done == nil || parsed.Summary == nil || parsed.Files == nil || parsed.Needs == nil {
		result.SetCode(run.CodeParse)
		return result, fmt.Errorf("invalid answer.json: required fields are missing or null")
	}
	result.Done = *parsed.Done
	result.Summary = *parsed.Summary
	result.Files = parsed.Files
	result.Needs = parsed.Needs
	result.Decisions = parsed.Decisions
	return result, nil
}

// skillArgv is a skill test's argv: the process prefix, built minimal and
// without a state folder, the admitted launch word -p, then the test's own
// flags.
func skillArgv(s run.Spec) []string {
	return s.Process.LaunchArgv("--model", s.Route.Model, "--effort", s.Route.Effort, "--tools", "", "--strict-mcp-config", "--no-session-persistence", "--disable-slash-commands", "--output-format", "json")
}

func skillResult(stdout []byte, result run.Result) (run.Result, error) {
	var output struct {
		Result  string      `json:"result"`
		IsError bool        `json:"is_error"`
		Usage   claudeUsage `json:"usage"`
	}
	if err := json.Unmarshal(stdout, &output); err != nil {
		result.RawAnswer = string(stdout)
		return result, err
	}
	result.RawAnswer = output.Result
	result.Usage = output.Usage.tokens()
	if output.IsError {
		return result, fmt.Errorf("Claude returned an error: %s", output.Result) //nolint:staticcheck // ST1005: Claude is a product name
	}
	return result, nil
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

// ResetTime parses the time a Claude limit message says the quota returns.
// When the text names none it returns kit.FallbackReset, never the zero time.
func ResetTime(text string, now time.Time) time.Time {
	if reset := resetTime(text, now); !reset.IsZero() {
		return reset
	}
	return kit.FallbackReset(text, now)
}
