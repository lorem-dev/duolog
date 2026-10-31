package runner

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"

	"github.com/lorem-dev/duolog/internal/mode"
)

func claudeArgv(s Spec) []string {
	args := []string{"claude", "-p", "--model", s.Route.Model}
	if s.Route.Effort != "" {
		args = append(args, "--effort", s.Route.Effort)
	}
	args = append(args, "--output-format", "json", "--json-schema", answerSchema)
	args = append(args, mode.Args("claude", s.Mode, s.Route.Sandbox)...)
	if s.ThreadID == "" {
		args = append(args, "--no-session-persistence")
	} else {
		args = append(args, "--resume", s.ThreadID)
	}
	if len(s.Allowed) > 0 {
		args = append(args, "--allowedTools")
		args = append(args, s.Allowed...)
	}
	if len(s.Disallowed) > 0 {
		args = append(args, "--disallowedTools")
		args = append(args, s.Disallowed...)
	}
	return append(args, "--", taskInstruction)
}

func claudeResult(path string, result *Result) (json.RawMessage, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	// Only stdout is decoded; stderr is retained separately in stdout.log.
	// Startup diagnostics can precede the JSON object. Preserve the first JSON
	// line and the remaining stream so pretty-printed results still decode.
	reader := bufio.NewReader(file)
	var first []byte
	for {
		line, readErr := reader.ReadBytes('\n')
		if bytes.HasPrefix(bytes.TrimSpace(line), []byte("{")) {
			first = line
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	var output struct {
		SessionID  string          `json:"session_id"`
		Structured json.RawMessage `json:"structured_output"`
		Usage      struct {
			Input    int64 `json:"input_tokens"`
			Creation int64 `json:"cache_creation_input_tokens"`
			Cached   int64 `json:"cache_read_input_tokens"`
			Output   int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(io.MultiReader(bytes.NewReader(first), reader)).Decode(&output); err != nil {
		return nil, err
	}
	result.SessionID = output.SessionID
	// Claude reports uncached, cache creation and cache reads separately; Input
	// is total input for both vendors, with Cached being the included read subset.
	result.Usage = Usage{Input: output.Usage.Input + output.Usage.Creation + output.Usage.Cached, Cached: output.Usage.Cached, Output: output.Usage.Output}
	if len(output.Structured) == 0 || bytes.Equal(output.Structured, []byte("null")) {
		return nil, io.ErrUnexpectedEOF
	}
	return output.Structured, nil
}
