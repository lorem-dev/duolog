package runner

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/quota"
)

// claudeArgv is the process prefix, which carries the worker's one --settings
// (auto memory off, instruction files out for minimal context, state roots
// denied to an enabled sandbox, and fast mode pinned off in the free
// variant), then the run's own flags. --setting-sources would do more, but
// it also drops the user settings that enable the duolog plugin and its gate.
func claudeArgv(s Spec) []string {
	args := s.Process.Argv("-p", "--model", s.Route.Model)
	if s.Route.Effort != "" {
		args = append(args, "--effort", s.Route.Effort)
	}
	// stream-json carries the rate limit events that tell whether the run spent
	// paid overage; -p accepts it only with --verbose.
	args = append(args, "--output-format", "stream-json", "--verbose", "--json-schema", answerSchema)
	if s.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(s.MaxTurns))
	}
	args = append(args, mode.Args("claude", s.Mode, s.Route.Sandbox)...)
	// In the cost study the user's MCP servers added 7.4 thousand tokens to every
	// call, so a worker gets them only when its task needs one. Bridged servers
	// add to the worker's own when it keeps them, and are the only ones otherwise.
	if !s.KeepMCP {
		args = append(args, "--strict-mcp-config")
	}
	// A worker cannot delegate. The gate denies a spawn anyway; without the tools
	// the worker does not spend turns trying.
	disallowed := []string{"Agent", "Task"}
	if s.MCP != nil {
		// These options take a list, so each gets one comma-joined value with =.
		if s.MCP.configPath != "" {
			args = append(args, "--mcp-config="+s.MCP.configPath)
		}
		if len(s.MCP.AllowedTools) > 0 {
			args = append(args, "--allowedTools="+strings.Join(s.MCP.AllowedTools, ","))
		}
		disallowed = append(disallowed, s.MCP.DisallowedTools...)
	}
	args = append(args, "--disallowedTools="+strings.Join(disallowed, ","))
	if s.Route.Context == "minimal" {
		// Minimal context drops every CLAUDE.md, the user's too, and skills unless the
		// task needs one: a project
		// under the home folder would otherwise load it as project instructions.
		if !s.KeepSkills {
			args = append(args, "--disable-slash-commands")
		}
	}
	if s.ThreadID == "" {
		if !s.Persistent {
			args = append(args, "--no-session-persistence")
		}
	} else {
		args = append(args, "--resume", s.ThreadID)
	}
	return append(args, "--", taskInstruction)
}

type claudeUsage struct {
	Input    int64 `json:"input_tokens"`
	Creation int64 `json:"cache_creation_input_tokens"`
	Cached   int64 `json:"cache_read_input_tokens"`
	Output   int64 `json:"output_tokens"`
}

// usage counts cache creation as input. Claude reports uncached, cache
// creation and cache reads separately; Input is total input for both vendors,
// with Cached being the included read subset.
func (u claudeUsage) usage() Usage {
	return Usage{Input: u.Input + u.Creation + u.Cached, Cached: u.Cached, Output: u.Output}
}

// claudeOutput is Claude's final result: the result event of stream-json, or
// the single object of the json output format.
type claudeOutput struct {
	SessionID string `json:"session_id"`
	// Subtype is error_max_turns when the run stopped at --max-turns.
	Subtype    string          `json:"subtype"`
	Structured json.RawMessage `json:"structured_output"`
	// A pointer, so that a result without usage is told from zero usage.
	Usage *claudeUsage    `json:"usage"`
	Cost  json.RawMessage `json:"total_cost_usd"`
}

// claudeStream folds the objects of Claude's stdout capture.
type claudeStream struct {
	// result is the last result event; legacy the first untyped object that
	// looks like a final result, from the json output format.
	result, legacy *claudeOutput
	evidence       quota.RunEvidence
	// messages holds the latest usage of each assistant message: stream-json
	// repeats a message's usage for each of its content blocks.
	messages map[string]Usage
	unnamed  Usage
	// known means an assistant message reported its usage, so the messages'
	// sum is a count even when it is zero.
	known bool
}

// add folds one object. Only the event types Claude itself writes count:
// tool output and model text reach the stream inside them, never as events.
func (s *claudeStream) add(object []byte) {
	var event struct {
		Type      *string         `json:"type"`
		Message   json.RawMessage `json:"message"`
		RateLimit json.RawMessage `json:"rate_limit_info"`
	}
	if json.Unmarshal(object, &event) != nil {
		return
	}
	if event.Type == nil {
		var output claudeOutput
		if s.legacy == nil && json.Unmarshal(object, &output) == nil && (output.SessionID != "" || len(output.Structured) > 0 || output.Usage != nil || output.Subtype != "") {
			s.legacy = &output
		}
		return
	}
	switch *event.Type {
	case "result":
		var output claudeOutput
		if json.Unmarshal(object, &output) == nil {
			s.result = &output
		}
	case "rate_limit_event":
		if e, ok := claudeRateLimit(event.RateLimit); ok {
			s.evidence.Add(e)
		}
	case "assistant":
		var message struct {
			ID    string       `json:"id"`
			Usage *claudeUsage `json:"usage"`
		}
		if json.Unmarshal(event.Message, &message) != nil || message.Usage == nil {
			return
		}
		s.known = true
		if message.ID == "" {
			s.unnamed = s.unnamed.add(message.Usage.usage())
			return
		}
		if s.messages == nil {
			s.messages = map[string]Usage{}
		}
		s.messages[message.ID] = message.Usage.usage()
	}
}

// usage is what the assistant messages so far used, for a run that ended
// before its result.
func (s *claudeStream) usage() Usage {
	total := s.unnamed
	for _, u := range s.messages {
		total = total.add(u)
	}
	return total
}

func (u Usage) add(v Usage) Usage {
	return Usage{Input: u.Input + v.Input, Cached: u.Cached + v.Cached, Output: u.Output + v.Output}
}

// readClaudeStream folds Claude's stdout capture at path into result: the
// session, usage, reported cost and billing evidence, whatever the run's exit,
// so a failed or cancelled run keeps what it measured. It returns the
// structured answer, ErrTurnCap with TurnsEnded for a run stopped at
// --max-turns, or an error when the capture holds no answer.
func readClaudeStream(path string, result *Result) (json.RawMessage, error) {
	// A stream-json run reports its rate limits. Seen stays false when it sent
	// none, and the run counts as charged.
	result.Billing.Expected = true
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	// Only stdout is decoded; stderr is retained separately in stdout.log.
	// Try each object-starting line independently so malformed diagnostics do
	// not hide the rest, while multiline JSON remains decodable.
	reader := bufio.NewReader(file)
	var stream claudeStream
	var offset int64
	var decodeErr error
	for {
		line, readErr := reader.ReadBytes('\n')
		next := offset + int64(len(line))
		if bytes.HasPrefix(bytes.TrimSpace(line), []byte("{")) {
			decoder := json.NewDecoder(io.NewSectionReader(file, offset, math.MaxInt64-offset))
			var object json.RawMessage
			if decodeErr = decoder.Decode(&object); decodeErr == nil {
				stream.add(object)
				// Continue after an object that spans lines.
				if end := offset + decoder.InputOffset(); end > next {
					if _, err = file.Seek(end, io.SeekStart); err != nil {
						return nil, err
					}
					reader.Reset(file)
					next, readErr = end, nil
				}
			}
		}
		offset = next
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	result.Billing.Add(stream.evidence)
	output := stream.result
	if output == nil {
		output = stream.legacy
	}
	if output == nil {
		result.Usage, result.UsageKnown = stream.usage(), stream.known
		if decodeErr != nil {
			return nil, decodeErr
		}
		return nil, io.ErrUnexpectedEOF
	}
	result.SessionID = output.SessionID
	// The result's usage is the run's total; the messages' usage is part of it.
	result.Usage, result.UsageKnown = stream.usage(), stream.known
	if output.Usage != nil {
		result.Usage, result.UsageKnown = output.Usage.usage(), true
	}
	result.ReportedUSD = reportedUSD(output.Cost)
	if output.Subtype == "error_max_turns" {
		result.TurnsEnded = true
		return nil, ErrTurnCap
	}
	if len(output.Structured) == 0 || bytes.Equal(output.Structured, []byte("null")) {
		return nil, io.ErrUnexpectedEOF
	}
	return output.Structured, nil
}
