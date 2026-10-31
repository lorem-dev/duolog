package session

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/lorem-dev/duolog/internal/mode"
)

// LeadState is the most recent native transcript observation. Empty fields are
// unknown; in particular, a missing effort must not cap a worker's effort.
type LeadState struct {
	Model, Effort, ApprovalPolicy string
	Mode                          mode.Mode
}

// ReadLeadState reads at most the final 64 KiB. Missing files and malformed or
// incomplete records are ordinary unknown observations, not hook failures.
func ReadLeadState(agent, transcriptPath string) (LeadState, error) {
	var state LeadState
	if agent != "claude" && agent != "codex" {
		return state, nil
	}
	f, err := os.Open(transcriptPath)
	if err != nil {
		return state, nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return state, nil
	}
	start := max(int64(0), info.Size()-64*1024)
	if _, err = f.Seek(start, io.SeekStart); err != nil {
		return state, nil
	}
	data, err := io.ReadAll(io.LimitReader(f, 64*1024))
	if err != nil {
		return state, nil
	}
	if start > 0 {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		} else {
			return state, nil
		}
	}
	var nullEffort bool
	for _, line := range bytes.SplitAfter(data, []byte{'\n'}) {
		if len(line) == 0 || line[len(line)-1] != '\n' {
			continue
		}
		var record struct {
			Type    string `json:"type"`
			Effort  string `json:"effort"`
			Message struct {
				Model string `json:"model"`
			} `json:"message"`
			Payload struct {
				Model    string          `json:"model"`
				Effort   json.RawMessage `json:"effort"`
				Approval string          `json:"approval_policy"`
				Sandbox  struct {
					Type string `json:"type"`
				} `json:"sandbox_policy"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &record) != nil {
			continue
		}
		if agent == "claude" && record.Type == "assistant" {
			state = LeadState{Model: record.Message.Model, Effort: record.Effort}
		}
		if agent == "codex" && record.Type == "turn_context" {
			state = LeadState{Model: record.Payload.Model, ApprovalPolicy: record.Payload.Approval}
			_ = json.Unmarshal(record.Payload.Effort, &state.Effort)
			nullEffort = bytes.Equal(bytes.TrimSpace(record.Payload.Effort), []byte("null"))
			switch record.Payload.Sandbox.Type {
			case "read-only":
				state.Mode = mode.Plan
			case "workspace-write":
				state.Mode = mode.Ask
			case "danger-full-access":
				state.Mode = mode.Full
			}
		}
	}
	if agent == "codex" && nullEffort && state.Model != "" {
		state.Effort = defaultEffort(state.Model)
	}
	return state, nil
}

func defaultEffort(model string) string {
	// Metadata does not invoke a model. Bound it so a broken installation cannot
	// hold up a lead hook indefinitely, and never inherit hook stdin.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", "debug", "models")
	cmd.Stdin = bytes.NewReader(nil)
	data, err := cmd.Output()
	if err != nil {
		return ""
	}
	var catalog struct {
		Models []struct {
			Slug    string `json:"slug"`
			Default string `json:"default_reasoning_level"`
		} `json:"models"`
	}
	if json.Unmarshal(data, &catalog) != nil {
		return ""
	}
	for _, entry := range catalog.Models {
		if entry.Slug == model {
			return entry.Default
		}
	}
	return ""
}

// Originator is a separate bounded header read: session_meta can be much older
// than the tail used for permission and effort decisions.
func Originator(transcriptPath string) string {
	f, err := os.Open(transcriptPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1024*1024))
	if err != nil {
		return ""
	}
	line, _, _ := bytes.Cut(data, []byte{'\n'})
	var record struct {
		Type    string `json:"type"`
		Payload struct {
			Originator string `json:"originator"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &record) != nil || record.Type != "session_meta" {
		return ""
	}
	return record.Payload.Originator
}
