package runner

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"math"
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
		if !s.Persistent {
			args = append(args, "--no-session-persistence")
		}
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
	// Try each object-starting line independently so malformed diagnostics do
	// not hide the result, while multiline JSON remains decodable.
	reader := bufio.NewReader(file)
	type claudeOutput struct {
		SessionID  string          `json:"session_id"`
		Structured json.RawMessage `json:"structured_output"`
		Usage      struct {
			Input    int64 `json:"input_tokens"`
			Creation int64 `json:"cache_creation_input_tokens"`
			Cached   int64 `json:"cache_read_input_tokens"`
			Output   int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	var output claudeOutput
	var offset int64
	var decodeErr error
	for {
		line, readErr := reader.ReadBytes('\n')
		if bytes.HasPrefix(bytes.TrimSpace(line), []byte("{")) {
			output = claudeOutput{}
			decodeErr = json.NewDecoder(io.NewSectionReader(file, offset, math.MaxInt64-offset)).Decode(&output)
			if decodeErr == nil {
				break
			}
		}
		offset += int64(len(line))
		if readErr != nil {
			if decodeErr != nil {
				return nil, decodeErr
			}
			return nil, readErr
		}
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
