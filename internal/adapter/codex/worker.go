package codex

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/adapter/kit/process"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/run"
)

const codexAnswerInstruction = "Reply with only one JSON object with the keys done, summary, files, needs and decisions. done is true or false, summary is a string, files, needs and decisions are arrays of strings."

// Worker runs Codex workers. It is the process.Dialect of Codex.
type Worker struct{}

// Run starts one Codex worker and reads its answer.
func (w Worker) Run(ctx context.Context, s run.Spec) (run.Result, error) {
	return process.Run(ctx, s, w)
}

// Launch is what a worker's process carries after its prefix, in
// workerenv.ProcessInput.Launch: exec and its sandbox flag with both pins, as
// the run puts them before resume. A skill test's run is exec with a read-only
// sandbox. The caller builds them into the process the gate admits; Run
// refuses a process whose launch words differ.
func (Worker) Launch(s run.Spec) []string {
	if s.SkillTest {
		return []string{"exec", "--sandbox", "read-only"}
	}
	return append([]string{"exec"}, permissionArgs(s)...)
}

// permissionArgs returns the native permission flags for a worker. It falls
// back to mode.Args when Native is empty, unknown, above Mode, or not the
// read-only sandbox's own default (read-only under a read-only sandbox). A
// workspace-write launch always pins network_access and writable_roots, so
// that Codex's own config files cannot widen the worker; the extras apply
// only on a resolved workspace-write at edits or full.
func permissionArgs(s run.Spec) []string {
	args, extras := mode.Args("codex", s.Mode, s.Route.Sandbox), false
	_, known := mode.Rank("codex", s.Native)
	switch {
	case s.Native == "" || !known || mode.Lower(mode.Parse("codex", s.Native), s.Mode) != mode.Parse("codex", s.Native):
	case s.Route.Sandbox == "read-only" && s.Native != mode.Default("codex", mode.Plan, "read-only"):
	default:
		args = mode.Flags("codex", s.Native)
		extras = s.Native == "workspace-write" && (s.Mode == mode.Edits || s.Mode == mode.Full)
	}
	if !extras {
		return mode.PinCodexSandbox("codex", args, false, nil)
	}
	return mode.PinCodexSandbox("codex", args, s.Network, s.WritableRoots)
}

// Argv includes the executable: the Process prefix once, with its global
// overrides, then the run's own flags. Route.Model is already resolved by the
// caller. Without a Process there is no argv.
func (Worker) Argv(s run.Spec) []string {
	if s.Process == nil {
		return nil
	}
	if s.SkillTest {
		return skillArgv(s)
	}
	return codexArgv(s)
}

// Stdin is the task block; --output-schema loses the prompt cache, so Codex is
// asked in plain text for the answer's shape.
func (Worker) Stdin(s run.Spec) []byte {
	if s.SkillTest {
		return s.Stdin
	}
	return append(append([]byte{}, s.Stdin...), []byte("\n"+codexAnswerInstruction+"\n")...)
}

// Prepare has nothing to write: Codex takes its MCP servers as -c overrides.
func (Worker) Prepare(string, *run.Spec) (func(), error) { return nil, nil }

// Scan reads the events of a run that was not interrupted: the thread and the
// usage of each completed turn. Codex writes its answer file itself.
func (Worker) Scan(stdout []byte, _ run.Spec, r *run.Result, interrupted bool) ([]byte, error) {
	if interrupted {
		return nil, nil
	}
	reader := bufio.NewReader(bytes.NewReader(stdout))
	for done := false; !done; {
		line, err := reader.ReadBytes('\n')
		codexEvent(line, r)
		done = err != nil
	}
	return nil, nil
}

// ErrorEvent: Codex has no error event beyond the shared types.
func (Worker) ErrorEvent(string, bool) bool { return false }

// mcpServerPattern names the server in Codex's MCP start-up errors.
var mcpServerPattern = regexp.MustCompile("MCP client for [`'\"]?([A-Za-z0-9_.-]+)[`'\"]? failed")

// AuthServer names the server in Codex's MCP start-up error.
func (Worker) AuthServer(text string) string {
	if m := mcpServerPattern.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

// Reset parses Codex's "try again at 8:48 AM".
func (Worker) Reset(text string, now time.Time) time.Time { return codexResetTime(text, now) }

// Fold reads the final message tolerantly, or for a skill test returns it raw.
func (Worker) Fold(_, answer []byte, answerErr error, s run.Spec, result run.Result) (run.Result, error) {
	if s.SkillTest {
		result.RawAnswer = string(answer)
		return result, answerErr
	}
	return codexAnswer(answer, filepath.Join(s.TaskDir, "answer.json"), result)
}

// skillArgv is a skill test's argv: the process prefix, built minimal and
// without a state folder, the admitted launch words (exec with a read-only
// sandbox), then the test's own flags.
func skillArgv(s run.Spec) []string {
	args := s.Process.LaunchArgv("--skip-git-repo-check", "--ephemeral", "-m", s.Route.Model, "-c", fmt.Sprintf("model_reasoning_effort=%q", s.Route.Effort), "-c", "project_doc_max_bytes=0", "-c", "skills.include_instructions=false", "-c", "features.shell_tool=false", "-c", "features.apply_patch_freeform=false", "-c", `web_search="disabled"`)
	if s.MCP != nil {
		args = append(args, s.MCP.CodexArgs...)
	}
	return append(args, "--json", "-o", filepath.Join(s.TaskDir, "answer.json"), "-")
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

// ResetTime parses the time a Codex limit message says the quota returns. When
// the text names none it returns kit.FallbackReset, never the zero time.
func ResetTime(text string, now time.Time) time.Time {
	if reset := codexResetTime(text, now); !reset.IsZero() {
		return reset
	}
	return kit.FallbackReset(text, now)
}
